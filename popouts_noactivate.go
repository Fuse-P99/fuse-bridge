package main

// Overlay windows must never activate as a side effect of Show(), z-order
// changes, or WebView2's NavigationCompleted re-Show: racers log in from
// character select and immediately foot-race, and even a millisecond focus
// steal breaks EQ's mouselook capture (camera spin) or eats a keystroke.
// WS_EX_NOACTIVATE makes every SW_SHOW and SetWindowPos non-activating by
// Win32 semantics — mouse input, dragging and click-through still work, the
// OS just never hands the window keyboard focus on its own. Deliberate
// activation (w.Focus → SetForegroundWindow) still works on a NOACTIVATE
// window per the documented style contract; that is what FocusPopout's
// click-to-type escalation uses.

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// setPopoutNoActivate adds or removes WS_EX_NOACTIVATE on an overlay window.
// Same read-modify-write shape as Wails' own setIgnoreMouseEvents (which
// touches only WS_EX_LAYERED|WS_EX_TRANSPARENT), so the two can never clobber
// each other's bits and the flag survives every lock/unlock cycle.
//
// Contract:
//   - Never call with popoutsMu held: SetWindowLong delivers WM_STYLECHANGED
//     to the window's (main) thread via SendMessage, and the main thread may
//     itself be waiting on popoutsMu.
//   - Overlay windows only — never the main window: players alt-tab to it
//     deliberately, and it is the single-instance second-launch Show target.
func setPopoutNoActivate(w *application.WebviewWindow, on bool) {
	if w == nil {
		return
	}
	// NativeWindow returns nil for a nil-impl or destroyed window, which
	// converts to 0 here — the one guard covers every failure mode.
	hwnd := w32.HWND(uintptr(w.NativeWindow()))
	if hwnd == 0 {
		return
	}
	ex := uint32(w32.GetWindowLong(hwnd, w32.GWL_EXSTYLE))
	if on {
		ex |= w32.WS_EX_NOACTIVATE
	} else {
		ex &^= w32.WS_EX_NOACTIVATE
	}
	w32.SetWindowLong(hwnd, w32.GWL_EXSTYLE, ex)
}

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

// popoutDragBegin / popoutDragEnd bracket a title-bar drag (or frameless edge
// resize) with real activation — the OS-modal move loop needs it.
//
// Dragging runs DefWindowProc's SC_MOVE loop, which normally ACTIVATES the
// window first. WS_EX_NOACTIVATE suppresses that, and a modal loop on a
// non-foreground thread is exactly the machine-wide input wedge users hit:
// the loop holds system-wide mouse capture, misses its button-up, and every
// click on the desktop — game and OS alike — is swallowed until an alt-tab
// forces WM_CANCELMODE. (Wails' own WM_ENTERSIZEMOVE → SetFocus →
// SetForegroundWindow chain fixes this only when Windows grants the
// foreground request; when it's denied the loop runs headless and wedges.
// Field signature: the first drag after touching FuseBridge works, the next
// one locks the screen.)
//
// So for the duration of the drag the overlay becomes a normal window: the
// NOACTIVATE bit comes off and it takes foreground deliberately — the user is
// physically holding its title bar, which is as deliberate as interaction
// gets, and the same click grants us the SetForegroundWindow permission. On
// exit the bit goes back on. No automatic give-back: pre-NOACTIVATE builds
// activated on drag too, and clicking the game returns focus as it always
// did.
//
// The hooks fire from Wails' WindowStartMove/StartResize events, emitted at
// WM_ENTERSIZEMOVE — loop entry — so activation lands before (or within a few
// ms of) the first pump. focuswatch's wedge watchdog remains the backstop for
// any path this bracket misses.
func popoutDragBegin(w *application.WebviewWindow, name string) {
	if w == nil {
		return
	}
	hwnd := w32.HWND(uintptr(w.NativeWindow()))
	if hwnd == 0 {
		return
	}
	setPopoutNoActivate(w, false)
	w32.SetForegroundWindow(hwnd)
	writeLog("overlay drag: activated " + name + " for the move loop")
}

func popoutDragEnd(w *application.WebviewWindow, name string) {
	if w == nil {
		return
	}
	setPopoutNoActivate(w, true)
	writeLog("overlay drag: done, " + name + " non-activating again")
}

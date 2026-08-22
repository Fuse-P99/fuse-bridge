package main

// ── UNWOUND 2026-08-22 (v2.5.3469): setPopoutNoActivate is NOT CALLED. ──────
//
// WS_EX_NOACTIVATE was applied to every overlay from v2.5.2469 to stop
// character-load bursts stealing the game's keyboard (racers), and it did fix
// that. But it also caused machine-wide input lockouts — all clicks eaten,
// game and OS, until an alt-tab — and the failure outlived BOTH repair
// attempts:
//   - 2.5.3269 bracketed the OS move loop with deliberate activation: field
//     said "no change".
//   - 2.5.3369 removed the OS move loop entirely (manual cursor-following
//     drag): the lockout STILL reproduced, and the capture watchdog
//     (focuswatch.go) never fired — so the stuck capture isn't a move loop
//     and isn't even on our UI thread. The remaining suspect is WebView2/
//     Chromium's own mouse-capture handling on a window that refuses
//     activation, which lives in the runtime process where we can neither
//     observe nor cancel it.
// Lesson for any future attempt: do NOT ship WS_EX_NOACTIVATE always-on
// under WebView2. If load-burst focus protection is revisited, it has to be
// scoped to the burst (timed, or gated on "EQ is foreground") and field-
// tested for click lockouts specifically — the failure needs FB backgrounded
// and real gameplay clicking, which dev-machine testing never exercised.
//
// The function is kept because the SW_SHOWNOACTIVATE module patch keys off
// the style bit (a window without the bit shows exactly as stock Wails), and
// so any future experiment has the tested primitive ready.

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

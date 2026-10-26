package main

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// Always-on-top self-heal.
//
// Field report (2026-08-29): a user's overlays all sat BEHIND the game while
// the app still believed always-on-top was on — re-opening an overlay's
// settings panel (gear → Done, no changes) fixed that one window, restarting
// the client fixed them all. Both "fixes" simply re-ran SetAlwaysOnTop, which
// means Windows had silently stripped WS_EX_TOPMOST from the windows.
// Windows does that in several documented-by-reports situations — display
// sleep/resume, resolution or DPI changes, fullscreen transitions — none of
// which we can prevent. So, like the visibility reconcile, we detect and
// repair: every watcher tick re-asserts topmost on any overlay that should
// have it and has lost it.
//
// The one thing that must NOT be re-asserted is a deliberate opt-out: each
// overlay's settings panel has its own "always on top" toggle, stored
// frontend-side (localStorage). Those toggles now route through the
// SetPopoutAlwaysOnTop binding below instead of calling the raw window API,
// so Go always knows the intent and the reconciler leaves opted-out overlays
// alone.

// popoutTopmostOff records overlays whose settings panel turned always-on-top
// OFF (windows are created with it on, so absence = wants topmost). Guarded
// by popoutsMu; entries die with the window (WindowClosing hook) — a reopened
// overlay starts topmost again and its frontend re-applies any stored opt-out
// on boot.
var popoutTopmostOff = map[string]bool{}

// SetPopoutAlwaysOnTop applies an overlay's always-on-top toggle and records
// the intent for the topmost reconciler. The overlay identifies itself by
// kind+category, same as SetPopoutSticky.
func (a *App) SetPopoutAlwaysOnTop(kind, category string, on bool) {
	name, _, _, _, _, _, _ := popoutIdent(kind, category)
	popoutsMu.Lock()
	if on {
		delete(popoutTopmostOff, name)
	} else {
		popoutTopmostOff[name] = true
	}
	w := popouts[name]
	popoutsMu.Unlock()
	if w != nil {
		w.SetAlwaysOnTop(on) // InvokeSyncs internally; safe off-main
	}
}

// popoutTopmostLogAt rate-limits the heal log per overlay: the repair itself
// runs every tick, but if some environment strips the bit persistently the
// log must not fill with a line every 500ms. Watcher goroutine only.
var popoutTopmostLogAt = map[string]time.Time{}

// reconcilePopoutTopmost re-asserts WS_EX_TOPMOST on any visible overlay that
// should be topmost but isn't. Runs on the focus-watcher tick; call WITHOUT
// popoutsMu held (SetWindowPos messages the UI thread, which may itself be
// waiting on the lock — same contract as setPopoutNoActivate).
func reconcilePopoutTopmost() {
	popoutsMu.Lock()
	wins := make(map[string]*application.WebviewWindow, len(popouts))
	for name, w := range popouts {
		if w != nil && !popoutTopmostOff[name] {
			wins[name] = w
		}
	}
	popoutsMu.Unlock()

	for name, w := range wins {
		hwnd := w32.HWND(uintptr(w.NativeWindow()))
		if hwnd == 0 {
			continue
		}
		// A hidden overlay (focus-hide, manual hide, auto-pause) is repaired
		// on the first tick after it shows again.
		if vis, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); vis == 0 {
			continue
		}
		ex := uint32(w32.GetWindowLong(hwnd, w32.GWL_EXSTYLE))
		if ex&w32.WS_EX_TOPMOST != 0 {
			continue
		}
		w32.SetWindowPos(hwnd, w32.HWND_TOPMOST, 0, 0, 0, 0,
			w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOACTIVATE)
		if time.Since(popoutTopmostLogAt[name]) > 5*time.Minute {
			popoutTopmostLogAt[name] = time.Now()
			writeLog("focuswatch: overlay " + name + " had lost always-on-top (OS stripped WS_EX_TOPMOST) — re-asserted")
		}
	}
}

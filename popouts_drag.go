package main

// Manual overlay dragging. The OS's own title-bar drag (WM_NCLBUTTONDOWN →
// DefWindowProc's modal SC_MOVE loop) is fundamentally unsafe on a
// WS_EX_NOACTIVATE window: the loop expects to run on an ACTIVE window, holds
// SYSTEM-WIDE mouse capture while it runs, and when Windows denies the
// activation it wants (our process not being foreground), it can miss its
// button-up and swallow every click on the machine — game and OS alike —
// until an alt-tab delivers WM_CANCELMODE. v2.5.3269 tried bracketing the
// loop with deliberate activation (popoutDragBegin); the field report was
// "no change". So the loop is now unreachable instead: the overlay title bar
// no longer carries --wails-draggable, and its pointerdown calls
// BeginPopoutDrag, which follows the physical cursor from a plain goroutine.
//
// No modal loop, no mouse capture, no activation, no focus change — the same
// ingredients that have made the JS resize grips wedge-proof all along. Done
// on the Go side rather than in JS so the units are exact: GetCursorPos and
// SetWindowPos both speak physical screen pixels, which sidesteps every
// CSS-zoom / devicePixelRatio / mixed-DPI-monitor conversion.
//
// The drag ends when the HARDWARE left-button state reads up
// (GetAsyncKeyState) — there is no message to miss and nothing to leak, which
// is the property the OS loop lacked.

import (
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// popoutDragActive: one drag at a time. A second pointerdown mid-drag (other
// mouse button, second mouse) is noise, not a second drag.
var popoutDragActive atomic.Bool

// BeginPopoutDrag starts a manual title-bar drag of one overlay. Bound; the
// popout shell calls it on title-bar pointerdown and nothing else.
func (a *App) BeginPopoutDrag(kind, category string) {
	name, _, _, _, _, _, _ := popoutIdent(kind, category)
	popoutsMu.Lock()
	w := popouts[name]
	popoutsMu.Unlock()
	if w == nil {
		return
	}
	if !popoutDragActive.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer popoutDragActive.Store(false)
		runPopoutDrag(w, name)
	}()
}

func runPopoutDrag(w *application.WebviewWindow, name string) {
	hwnd := w32.HWND(uintptr(w.NativeWindow()))
	if hwnd == 0 {
		return
	}
	startCX, startCY, ok := w32.GetCursorPos()
	if !ok {
		return
	}
	r := w32.GetWindowRect(hwnd)
	if r == nil {
		return
	}
	startWX, startWY := int(r.Left), int(r.Top)
	writeLog("overlay drag: manual move of " + name)

	// ~125Hz keeps the window pinned to the cursor. SetWindowPos with
	// NOACTIVATE|NOSIZE|NOZORDER is the same call racing-mode reposition uses,
	// just on a faster clock; it is safe from any thread.
	t := time.NewTicker(8 * time.Millisecond)
	defer t.Stop()
	// Absurdity backstop: no real drag lasts a minute. If the button state
	// somehow reads held forever (remote-desktop weirdness), stop moving the
	// window rather than shadow the cursor for the rest of the session.
	deadline := time.Now().Add(60 * time.Second)
	for range t.C {
		if time.Now().After(deadline) || !w32.IsWindow(hwnd) {
			return
		}
		// Hardware state, bit 15 = held right now. A quick click (no drag
		// intended) reads up on the first tick and moves nothing.
		if down, _, _ := procAsyncKeyState.Call(vkLButton); down&0x8000 == 0 {
			return
		}
		cx, cy, ok := w32.GetCursorPos()
		if !ok {
			continue
		}
		w32.SetWindowPos(hwnd, 0,
			startWX+(cx-startCX), startWY+(cy-startCY), 0, 0,
			w32.SWP_NOSIZE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE)
	}
}

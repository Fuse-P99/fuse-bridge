package main

// The hover card: one small, transient, click-through window that draws the
// shared item/member card next to the cursor on behalf of whichever overlay
// the mouse is over.
//
// Why it is a window of its own. A card drawn INSIDE an overlay is clipped by
// that overlay's OS window — a webview cannot paint outside one — and the
// Guild Chat overlay is deliberately small and usually parked low on the
// screen, over the game's own chat box, which is exactly where a 200px card
// has no room. So the card lives in a frameless, transparent, always-on-top
// window that is moved next to the cursor for each hover.
//
// It is NEVER registered in `popouts`. Everything in popouts.go treats those
// entries as the user's own overlays — locked, positioned, persisted,
// reopened per character — and none of that applies to something that exists
// for as long as a mouse sits still. The one exception is the global hide: an
// overlay set going away mid-hover must take the card with it, so
// reconcilePopoutVisibility hides it. Hide only — nothing but a hover ever
// shows it.
//
// ── coordinate space ────────────────────────────────────────────────────────
// The overlay reports the cursor in CSS pixels (clientX/clientY) and the card
// is placed with WebviewWindow.SetPosition, so the two have to agree.
//
// On Windows, Position/SetPosition are DIP: windowsWebviewWindow.bounds() is
// PhysicalToDipRect(physicalBounds()), and Screen.Bounds/WorkArea are laid out
// in that same DIP space (screenmanager.go, applyDPIScaling). The webview is
// hosted windowed — Options.Windows.UseVisualHosting is unset in app.go — and
// in that mode Wails leaves ShouldDetectMonitorScaleChanges ON, which makes
// WebView2 keep its own rasterization scale at the monitor's DPI/96. One CSS
// pixel is then one DIP, so cursor pixels add to a window position with NO
// scale factor applied. (The client already assumes that identity elsewhere:
// racing.go drops eqclient.ini's pixel rect straight into SetPosition, and
// Popout.svelte hands Window.Position()'s numbers to Go as saved geometry.)
// The placement is clamped to the screen regardless, so a machine that ever
// disagreed would put the card somewhere plain rather than off-screen.

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	hoverCardWindowName = "popout-hovercard"

	// Logical size. Tall enough for the biggest card the page can draw (a
	// fully-statted item with sales history and a held-by footer); the unused
	// area is transparent, so the slack costs nothing on screen.
	hoverCardW = 300
	hoverCardH = 480

	// Gap between the cursor and the card's nearest corner.
	hoverCardPad = 16

	// A hover that never gets its mouseleave — the overlay was hidden or
	// closed mid-hover, the page reloaded — must not strand a card on screen.
	hoverCardMaxShow = 12 * time.Second
)

// hoverCardReplays re-send the request shortly after the window is CREATED:
// the page is still loading then, so the first Emit has nobody listening and
// the very first hover of a session would draw an empty card. Stale replays
// drop themselves on the sequence number, and none of this runs again once
// the window exists.
var hoverCardReplays = []time.Duration{
	250 * time.Millisecond,
	600 * time.Millisecond,
	1200 * time.Millisecond,
	2000 * time.Millisecond,
}

var (
	// hoverCardMu guards everything below. Unlike popoutsMu (see
	// popouts_noactivate.go) it is never taken by the main thread, so holding
	// it across window creation cannot deadlock; the other window calls are
	// kept outside it anyway, since that costs nothing.
	hoverCardMu  sync.Mutex
	hoverCardWin *application.WebviewWindow
	// hoverCardSeq identifies the request currently on screen. Every show and
	// every hide bumps it, which is what cancels pending replays and a safety
	// timer armed for an older hover.
	hoverCardSeq uint64
	// hoverCardShown makes "hide it" free when there is nothing to hide: the
	// focus watcher reconciles overlay visibility twice a second, and each of
	// those passes would otherwise cost a Hide() round-trip to the main thread
	// for a window that has been down for hours.
	hoverCardShown bool
	hoverCardTimer *time.Timer
	// hoverCardLast is what the card should be showing right now — the payload
	// the replays below re-send, so a replay always carries the CURRENT hover
	// rather than the one that happened to create the window.
	hoverCardLast map[string]any
)

// ShowHoverCard positions the card window next to the cursor and tells its
// page what to draw. fromWindow is the popouts name of the overlay the cursor
// is over (its position anchors the cursor's CSS coordinates), kind is "item"
// or "member", and query is the item name or member lookup key. Bound.
func (a *App) ShowHoverCard(fromWindow, kind, query string, cx, cy float64) {
	if query == "" || !popoutsCanOpen() {
		return
	}
	popoutsMu.Lock()
	src := popouts[fromWindow]
	popoutsMu.Unlock()
	// Every window call from here on runs with popoutsMu released — the
	// popouts_noactivate.go contract: they SendMessage the main thread, which
	// may itself be waiting on that lock.
	if src == nil {
		return
	}

	ox, oy := src.Position()
	x := ox + int(cx) + hoverCardPad
	y := oy + int(cy) + hoverCardPad
	// The page draws the card in the corner of this window NEAREST the cursor
	// (these two flags), so a flipped card still touches the cursor instead of
	// hanging a window-height away from it.
	flipX, flipY := false, false
	if scr, err := src.GetScreen(); err == nil && scr != nil {
		wa := scr.WorkArea
		if wa.Width > 0 && wa.Height > 0 {
			if x+hoverCardW > wa.X+wa.Width {
				flipX = true
				x = ox + int(cx) - hoverCardPad - hoverCardW
			}
			if y+hoverCardH > wa.Y+wa.Height {
				flipY = true
				y = oy + int(cy) - hoverCardPad - hoverCardH
			}
			x = max(wa.X, min(x, wa.X+wa.Width-hoverCardW))
			y = max(wa.Y, min(y, wa.Y+wa.Height-hoverCardH))
		}
	}

	w, created := hoverCardWindow()
	if w == nil {
		return
	}
	payload := map[string]any{
		"kind": kind, "query": query, "flip_x": flipX, "flip_y": flipY,
	}

	hoverCardMu.Lock()
	hoverCardSeq++
	seq := hoverCardSeq
	hoverCardShown = true
	hoverCardLast = payload
	if hoverCardTimer != nil {
		hoverCardTimer.Stop()
	}
	hoverCardTimer = time.AfterFunc(hoverCardMaxShow, func() { hideHoverCardIf(seq) })
	hoverCardMu.Unlock()

	if created {
		// Click-through before the window is ever shown, so it can never eat a
		// click meant for the game or for the overlay underneath it.
		w.SetIgnoreMouseEvents(true)
	}
	w.SetPosition(x, y)
	// Emitted before the show so the page has the new content in hand by the
	// time the window appears. App events reach every window; only the card
	// page listens for this one.
	v3App.Event.Emit("hovercard", payload)

	prevFG := foregroundHWND()
	w.Show()
	if created {
		// Creating a window activates it once whatever the no-steal-show
		// registry does with later shows — hand the game its keyboard back.
		restoreForegroundTo(prevFG)
		for _, d := range hoverCardReplays {
			time.AfterFunc(d, replayHoverCard)
		}
	}
}

// HideHoverCard puts the card away: the cursor left whatever it was over.
// Bound.
func (a *App) HideHoverCard() { hideHoverCard() }

// hideHoverCard hides the card window whatever is on it.
func hideHoverCard() { hideHoverCardIf(0) }

// hideHoverCardIf hides the card window, but only while seq is still the
// request on screen (0 means unconditionally). Bumping the sequence is what
// retires the safety timer and any pending replay.
func hideHoverCardIf(seq uint64) {
	hoverCardMu.Lock()
	if !hoverCardShown || (seq != 0 && hoverCardSeq != seq) {
		hoverCardMu.Unlock()
		return
	}
	hoverCardShown = false
	hoverCardSeq++
	if hoverCardTimer != nil {
		hoverCardTimer.Stop()
		hoverCardTimer = nil
	}
	w := hoverCardWin
	hoverCardMu.Unlock()
	if w != nil {
		w.Hide()
	}
}

// replayHoverCard re-sends whatever the card should be showing, for a page
// that may have been too young to hear it the first time. No-op once the
// hover is over.
func replayHoverCard() {
	hoverCardMu.Lock()
	payload := hoverCardLast
	if !hoverCardShown {
		payload = nil
	}
	hoverCardMu.Unlock()
	if payload == nil || v3App == nil {
		return
	}
	v3App.Event.Emit("hovercard", payload)
}

// hoverCardWindow returns the card window, creating it on first use, and says
// whether this call is the one that created it. The window is kept for the
// app's lifetime: standing up a WebView per hover is far too slow for
// something that has to behave like a tooltip.
//
// Window options mirror openPopoutWindow's overlays — frameless, transparent
// composition, always on top, off the taskbar, no DWM decorations — plus the
// same no-steal-show registration, so a card appearing under the cursor never
// takes the game's keyboard.
func hoverCardWindow() (*application.WebviewWindow, bool) {
	hoverCardMu.Lock()
	defer hoverCardMu.Unlock()
	if hoverCardWin != nil {
		return hoverCardWin, false
	}
	// Not merely a nil check — see popoutsCanOpen: a window created before the
	// main loop runs comes up without transparent composition.
	if !popoutsCanOpen() {
		return nil, false
	}
	w := v3App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   hoverCardWindowName,
		Title:  "Hover Card",
		Width:  hoverCardW,
		Height: hoverCardH,
		// Nothing resizes this window, the user least of all — it has no
		// title bar, no grip, and no saved geometry.
		DisableResize: true,
		// Born hidden: the first ShowHoverCard positions it before showing it,
		// so it never flashes wherever the OS would have put it.
		Hidden:          true,
		InitialPosition: application.WindowCentered,
		Frameless:       true,
		AlwaysOnTop:     true,
		BackgroundType:  application.BackgroundTypeTransparent,
		URL:             "/#popout=hovercard",
		Windows: application.WindowsWindow{
			Theme:                             application.Dark,
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
		},
	})
	application.FuseBridgeSetNoStealShow(uintptr(w.NativeWindow()), true)
	hoverCardWin = w
	return w, true
}

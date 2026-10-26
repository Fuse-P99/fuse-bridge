package main

// Racing Mode overlay placement. Unlike every other overlay, this window is
// positioned by the app, not the user: it must cover the game's 3D viewport
// exactly so the racing lines land at true viewport percentages. The rect
// comes from eqclient.ini + the character's UI ini (eqini.go), and is
// re-checked on a slow tick so an EQ settings change or character swap walks
// the window to the right place within a few seconds.

import (
	"sync"
	"time"
)

var (
	racingMu     sync.Mutex
	racingLastAt time.Time
	racingRect   [4]int
)

// racingTick is called from the focus watcher's 500ms loop; the actual ini
// stat/reposition work runs every few seconds at most.
func racingTick() {
	racingMu.Lock()
	if time.Since(racingLastAt) < 3*time.Second {
		racingMu.Unlock()
		return
	}
	racingLastAt = time.Now()
	racingMu.Unlock()
	racingReposition(false)
}

// racingReposition snaps the racing overlay window onto the game viewport.
// No-ops when the window isn't open or the inis can't produce a rect (the
// window then just stays where it spawned).
func racingReposition(force bool) {
	popoutsMu.Lock()
	w := popouts["popout-racing"]
	popoutsMu.Unlock()
	if w == nil {
		return
	}
	x, y, wd, ht, ok := RacingOverlayRect()
	if !ok {
		return
	}
	rect := [4]int{x, y, wd, ht}
	racingMu.Lock()
	changed := force || rect != racingRect
	racingRect = rect
	racingMu.Unlock()
	if !changed {
		return
	}
	w.SetPosition(x, y)
	w.SetSize(wd, ht)
}

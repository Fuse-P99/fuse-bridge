package main

// Racing Mode overlay placement. Unlike every other overlay, this window is
// positioned by the app, not the user: it must cover the game's 3D viewport
// exactly so the racing lines land at true viewport percentages. The rect
// comes from eqclient.ini + the character's UI ini (eqini.go), and is
// re-checked on a slow tick so an EQ settings change or character swap walks
// the window to the right place within a few seconds.

import (
	"regexp"
	"strings"
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

// ── auto-close ──────────────────────────────────────────────────────────────
// Racing Mode is a for-the-race overlay: zoning ends the race, and a mob
// engaging the racer means they're in a fight, not a race. Both close the
// overlay outright (a user close — it won't reopen on next launch).

// "a shady goblin engages Deuce!"
var racingEngageRE = regexp.MustCompile(`^(.+?) engages ([A-Za-z]+)!$`)

// racingAutoClose closes the racing overlay if it's open, with a status note
// saying why. The window-closing hook records it closed like any user close.
func racingAutoClose(reason string) {
	popoutsMu.Lock()
	w := popouts["popout-racing"]
	popoutsMu.Unlock()
	if w == nil {
		return
	}
	addStatus("Racing Mode overlay closed — %s", reason)
	w.Close()
}

// RecordRacingLine watches the log for a mob engaging the racer. Called for
// every line, so the cheap contains gate leads.
func RecordRacingLine(line string) {
	content := logMessageContent(line)
	if !strings.HasSuffix(content, "!") || !strings.Contains(content, " engages ") {
		return
	}
	m := racingEngageRE.FindStringSubmatch(content)
	if m == nil || currentCharName == "" || !strings.EqualFold(m[2], currentCharName) {
		return
	}
	racingAutoClose(m[1] + " engaged you")
}

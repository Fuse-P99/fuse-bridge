package main

import (
	"sync"
	"time"
)

// Auto-open the Map tab when the player spams /loc — a quick, deliberate gesture
// to say "show me the map." Five /locs within five seconds triggers it, with a
// cooldown so holding the key down doesn't re-fire repeatedly.

const (
	autoMapLocCount  = 5
	autoMapLocWindow = 5 * time.Second
	autoMapCooldown  = 10 * time.Second
)

var (
	autoMapMu       sync.Mutex
	autoMapLocTimes []time.Time
	autoMapLastFire time.Time
)

// noteLocForAutoMap records a /loc from the local player and opens the Map tab
// once autoMapLocCount locs land within autoMapLocWindow. Called for every local
// /loc reading.
func noteLocForAutoMap() {
	now := time.Now()

	autoMapMu.Lock()
	cutoff := now.Add(-autoMapLocWindow)
	kept := autoMapLocTimes[:0]
	for _, t := range autoMapLocTimes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	autoMapLocTimes = append(kept, now)
	fire := len(autoMapLocTimes) >= autoMapLocCount && now.Sub(autoMapLastFire) > autoMapCooldown
	if fire {
		autoMapLastFire = now
		autoMapLocTimes = autoMapLocTimes[:0]
	}
	autoMapMu.Unlock()

	if fire && wailsApp != nil {
		wailsApp.OpenMap()
	}
}

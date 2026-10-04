package main

// Corpse markers: when a local character dies, the map drops a tombstone where
// the game last placed them, so the corpse run has a target.
//
// EQ never prints a location with the death message, so this leans entirely on
// the most recent /loc. A marker is only placed when that fix is fresh
// (corpseLocMaxAge) AND was taken in the zone the death happened in — a /loc
// from the zone you left five minutes ago says nothing about where you just
// died.
//
// Markers are keyed by character but shared across the install: log in the alt
// with the rez stick and your dead main's tombstone is still on the map. One
// clears when that character takes a rez, when that character stands within
// corpseClearRadius of it (you're on top of the body — you found it), or after
// corpseTTL. They persist to disk because corpseTTL outlives most app sessions.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Corpse is one tombstone marker on the map.
type Corpse struct {
	Char   string  `json:"char"`
	Zone   string  `json:"zone"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
	DiedAt int64   `json:"died_at"` // unix millis
}

const (
	corpseLocMaxAge   = 15 * time.Minute // a /loc older than this can't place a marker
	corpseTTL         = 3 * time.Hour    // markers expire on their own
	corpseClearRadius = 50.0             // loc units; standing this close clears it
	corpseMax         = 40               // bound the file when someone has a bad night
	corpseRezLine     = "You regain some experience from resurrection."
)

// Death lines. The "<name> has been slain by" form is what EQ prints for other
// players; it's matched against the active character so a log that names you
// instead of saying "You" still registers.
var (
	corpseSelfDeathRE  = regexp.MustCompile(`^You have been slain by `)
	corpseNamedDeathRE = regexp.MustCompile("^([\\w`' -]+) has been slain by ")
)

var (
	corpseMu sync.Mutex
	corpses  []Corpse
)

func corpsePath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "corpses.json")
}

// LoadCorpses restores saved tombstones at startup, dropping any that expired
// while the app was closed. Call once from main.
func LoadCorpses() {
	data, err := os.ReadFile(corpsePath())
	if err != nil {
		return
	}
	var list []Corpse
	if json.Unmarshal(data, &list) != nil {
		return
	}
	corpseMu.Lock()
	corpses = list
	n := pruneCorpsesLocked()
	corpseMu.Unlock()
	if n > 0 {
		addStatus("Corpse markers: restored %d", n)
	}
}

// saveCorpsesLocked writes the current list. Caller holds corpseMu.
func saveCorpsesLocked() {
	path := corpsePath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(corpses, "", "  ")
	_ = os.WriteFile(path, data, 0600)
}

// pruneCorpsesLocked drops expired markers and trims to corpseMax (oldest
// first), returning how many remain. Caller holds corpseMu.
func pruneCorpsesLocked() int {
	cutoff := time.Now().Add(-corpseTTL).UnixMilli()
	kept := corpses[:0]
	for _, c := range corpses {
		if c.DiedAt > cutoff {
			kept = append(kept, c)
		}
	}
	corpses = kept
	if len(corpses) > corpseMax {
		corpses = corpses[len(corpses)-corpseMax:]
	}
	return len(corpses)
}

// corpseDist is the 3D distance between a marker and a point. Elevation counts:
// dying at the top of a spire and walking underneath it shouldn't clear the
// marker, and 50 units is loose enough that standing on the body still does.
func corpseDist(c Corpse, x, y, z float64) float64 {
	return math.Sqrt((c.X-x)*(c.X-x) + (c.Y-y)*(c.Y-y) + (c.Z-z)*(c.Z-z))
}

func sameChar(a, b string) bool { return strings.EqualFold(a, b) }
func sameZone(a, b string) bool {
	return normalizeZoneKey(a) != "" && normalizeZoneKey(a) == normalizeZoneKey(b)
}

// RecordCorpseLine watches the log for the local player's death and rez. Call
// for every raw line.
func RecordCorpseLine(line string) {
	char := currentCharName
	if char == "" {
		return
	}
	content := logMessageContent(line)
	switch {
	case corpseSelfDeathRE.MatchString(content):
		recordCorpseDeath(char)
	case content == corpseRezLine:
		clearCorpseOnRez(char)
	default:
		// Only pay for the second regex when the line could be a death.
		if strings.Contains(content, " has been slain by ") {
			if m := corpseNamedDeathRE.FindStringSubmatch(content); m != nil && sameChar(m[1], char) {
				recordCorpseDeath(char)
			}
		}
	}
}

// recordCorpseDeath drops a tombstone at the last known position, if that
// position is recent enough and from this zone to mean anything.
func recordCorpseDeath(char string) {
	p := GetPosition()
	zone := CurrentZone()
	if p.Time == 0 || p.Zone == "" {
		addStatus("Corpse: %s died, but no /loc has been seen — no marker placed.", char)
		return
	}
	if !sameZone(p.Zone, zone) {
		addStatus("Corpse: %s died in %s but the last /loc was in %s — no marker placed.", char, zone, p.Zone)
		return
	}
	if age := time.Since(time.UnixMilli(p.Time)); age > corpseLocMaxAge {
		addStatus("Corpse: %s died, but the last /loc is %s old — no marker placed.", char, age.Round(time.Minute))
		return
	}

	c := Corpse{Char: char, Zone: p.Zone, X: p.X, Y: p.Y, Z: p.Z, DiedAt: time.Now().UnixMilli()}
	corpseMu.Lock()
	// Dying twice on the same spot is one pile as far as the map is concerned —
	// refresh the existing marker instead of stacking an identical one.
	replaced := false
	for i := range corpses {
		if sameChar(corpses[i].Char, char) && sameZone(corpses[i].Zone, c.Zone) &&
			corpseDist(corpses[i], c.X, c.Y, c.Z) <= corpseClearRadius {
			corpses[i] = c
			replaced = true
			break
		}
	}
	if !replaced {
		corpses = append(corpses, c)
	}
	pruneCorpsesLocked()
	saveCorpsesLocked()
	corpseMu.Unlock()
	addStatus("Corpse: marked %s's corpse in %s at %.0f, %.0f", char, c.Zone, c.Y, c.X)
}

// clearCorpseOnRez removes the marker a rez just recovered. A rez always lands
// you on the corpse, so the one in the current zone is the right target; with
// several there, the newest is the best guess (nothing in the log identifies
// which body the cleric targeted).
func clearCorpseOnRez(char string) {
	zone := CurrentZone()
	corpseMu.Lock()
	best := -1
	for i, c := range corpses {
		if !sameChar(c.Char, char) {
			continue
		}
		// With no zone known, fall back to the newest marker for this character.
		if zone != "" && !sameZone(c.Zone, zone) {
			continue
		}
		if best < 0 || c.DiedAt > corpses[best].DiedAt {
			best = i
		}
	}
	if best < 0 {
		corpseMu.Unlock()
		return
	}
	c := corpses[best]
	corpses = append(corpses[:best], corpses[best+1:]...)
	saveCorpsesLocked()
	corpseMu.Unlock()
	addStatus("Corpse: %s was resurrected — cleared the marker in %s", char, c.Zone)
}

// CorpseCheckLoc clears any of this character's markers they're now standing
// on. Call from the /loc handler with the freshly parsed coordinates.
func CorpseCheckLoc(char string, x, y, z float64) {
	if char == "" {
		return
	}
	zone := CurrentZone()
	if zone == "" {
		return
	}
	corpseMu.Lock()
	kept := corpses[:0]
	cleared := 0
	for _, c := range corpses {
		if sameChar(c.Char, char) && sameZone(c.Zone, zone) &&
			corpseDist(c, x, y, z) <= corpseClearRadius {
			cleared++
			continue
		}
		kept = append(kept, c)
	}
	corpses = kept
	if cleared > 0 {
		saveCorpsesLocked()
	}
	corpseMu.Unlock()
	if cleared > 0 {
		addStatus("Corpse: %s reached their corpse in %s — cleared %d marker(s)", char, zone, cleared)
	}
}

// GetCorpses returns the live tombstones for a zone (every character on this
// install), newest first, expiring any that have run out the clock.
func (a *App) GetCorpses(zone string) []Corpse {
	out := []Corpse{}
	if strings.TrimSpace(zone) == "" {
		return out
	}
	corpseMu.Lock()
	before := len(corpses)
	pruneCorpsesLocked()
	if len(corpses) != before {
		saveCorpsesLocked()
	}
	for _, c := range corpses {
		if sameZone(c.Zone, zone) {
			out = append(out, c)
		}
	}
	corpseMu.Unlock()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

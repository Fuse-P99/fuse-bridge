package main

// Shared raid-AE anchors, client half. A few AoEs worth a raid-wide cooldown
// bar have a radius so small that most of the raid never logs the message —
// everyone ducks out, a handful of melee stay in. Those clients forward the
// landing/resist/fear lines and the server folds the copies into ONE shared
// anchor everybody's "Raid Specific Timers" window renders (raidAE.go there).
// The raid's spatial breadth becomes information every client gets to use.
//
// The zone gate here is load-bearing, not an optimisation: "Your life force
// drains away." is every lifetap in the game and "You flee in terror." every
// fear — only standing in the mob's own zone makes them Vulak's or
// Klandicar's. Dedupe of the many copies lives server-side.

import "regexp"

type raidAEForwardDef struct {
	zoneKey string // normalizeZoneKey of the mob's home zone
	re      *regexp.Regexp
}

var raidAEForwardDefs = []raidAEForwardDef{
	// Vulak`Aerr — Ancient Breath (NToV).
	{"templeofveeshan", regexp.MustCompile(`^(?:You resist the Ancient Breath Spell!|Your life force drains away\.(?:  You have taken \d+ points? of damage\.)?)$`)},
	// Dain Frostreaver IV — pit banish. Both shapes are Dain-specific.
	{"icewellkeep", regexp.MustCompile("^(?:[\\w`' -]+ is cast into the pit by Dain Frostreavers justice|The justice of Dain Frostreaver casts you into the pit)\\.$")},
	// Klandicar — Dragon Roar (fear).
	{"westernwastes", regexp.MustCompile(`^You (?:flee in terror\.|lose control of yourself!|resist the Dragon Roar spell!)$`)},
}

// raidAEForwardLine reports whether a raw log line is a raid-AE anchor worth
// forwarding: a pattern match while standing in that mob's zone. The zone
// check runs first, so for everyone outside the three zones this costs a few
// string compares per line.
func raidAEForwardLine(line string) bool {
	zkey := normalizeZoneKey(CurrentZone())
	if zkey == "" {
		return false
	}
	content := ""
	for i := range raidAEForwardDefs {
		d := &raidAEForwardDefs[i]
		if d.zoneKey != zkey {
			continue
		}
		if content == "" {
			content = logMessageContent(line)
		}
		if d.re.MatchString(content) {
			return true
		}
	}
	return false
}

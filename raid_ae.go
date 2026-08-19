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
	// Lord Yelinak — Ice Breath (Skyshrine). Landed, resist, and the
	// third-party shape, all Ice-Breath-specific wordings.
	{"skyshrine", regexp.MustCompile("^(?:You resist the Ice Breath [Ss]pell!|Shards of magical ice rend you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body is cut by shards of magical ice\\.)$")},
	// Aaryonar — Cloud of Disempowerment (NToV).
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Cloud of Disempowerment [Ss]pell!|You feel your skin freeze\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s skin freezes\\.)$")},
	// Lord Vyemm / Sevalak — Scream of Chaos (NToV).
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Scream of Chaos [Ss]pell!|You experience chaotic weightlessness\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+ rises chaotically into the air\\.)$")},
	// Zlexak — Diseased Cloud (NToV). Same zone gate keeps Zlandicar's
	// similar rot wording (Dragon Necropolis) from ever forwarding here.
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Diseased Cloud [Ss]pell!|Your body begins to rot\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body begins to rot\\.)$")},
	// Jorlleag (NToV) / Gozzrem (WToV) — Frost Breath.
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Frost Breath [Ss]pell!|Your body freezes as the frost hits you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body freezes as the frost hits them\\.)$")},
	// Lord Koi`Doken — Tsunami (NToV).
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Tsunami [Ss]pell!|A tsunami crushes you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+ is crushed by a wall of water\\.)$")},
	// Lady Mirenilla — Cloud of Fear (NToV).
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Cloud of Fear [Ss]pell!|Your mind is wracked by fear\\.|[\\w`' -]+ looks very afraid\\.)$")},
	// Lord Kreizenn — Wave of Flame (NToV).
	{"templeofveeshan", regexp.MustCompile("^(?:You resist the Wave of Flame [Ss]pell!|You feel your skin burn\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s skin burns\\.)$")},
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

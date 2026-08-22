package main

// Shared raid-AE anchors, client half. A few raid countdowns are worth sharing
// raid-wide because the line that starts them lands in ONE person's log: an AE
// with a radius so small most of the raid ducks out, a banish that teleports a
// single player, or a trash kill only the puller sees. Those clients forward
// the landing/resist/slain lines and the server folds the copies into ONE
// shared anchor everybody's "Raid Specific Timers" window renders (raidAE.go
// there). The raid's spatial breadth becomes information every client gets.
//
// The zone gate here is load-bearing, not an optimisation: "Your life force
// drains away." is every lifetap in the game, "You flee in terror." every fear,
// and "You have been teleported." every port in Norrath — only standing in the
// mob's own zone makes them Vulak's, Wuoshi's, or Trakanon's. Dedupe of the
// many copies lives server-side.
//
// Keep these in lockstep with raidAEDefs in raidAE.go: same wordings, and a
// zone here for every def there.

import "regexp"

type raidAEForwardDef struct {
	// zoneKeys are normalizeZoneKey forms of the mob's home zone. A list
	// because a zone's canonical name can differ by a plural we don't control
	// (The Wakening Land / Lands) and guessing wrong silently disables the
	// forward — the cost of carrying both spellings is a string compare.
	zoneKeys []string
	re       *regexp.Regexp
}

var raidAEForwardDefs = []raidAEForwardDef{
	// ── Temple of Veeshan (NToV) ──
	// Vulak`Aerr — Ancient Breath.
	{[]string{"templeofveeshan"}, regexp.MustCompile(`^(?:You resist the Ancient Breath Spell!|Your life force drains away\.(?:  You have taken \d+ points? of damage\.)?)$`)},
	// Aaryonar — Cloud of Disempowerment.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Cloud of Disempowerment [Ss]pell!|You feel your skin freeze\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s skin freezes\\.)$")},
	// Lord Vyemm / Sevalak — Scream of Chaos.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Scream of Chaos [Ss]pell!|You experience chaotic weightlessness\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+ rises chaotically into the air\\.)$")},
	// Zlexak — Diseased Cloud. Same zone gate keeps Zlandicar's similar rot
	// wording (Dragon Necropolis) from ever forwarding here.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Diseased Cloud [Ss]pell!|Your body begins to rot\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body begins to rot\\.)$")},
	// Jorlleag (NToV) / Gozzrem (WToV) — Frost Breath.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Frost Breath [Ss]pell!|Your body freezes as the frost hits you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body freezes as the frost hits them\\.)$")},
	// Lord Koi`Doken — Tsunami.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Tsunami [Ss]pell!|A tsunami crushes you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+ is crushed by a wall of water\\.)$")},
	// Lady Mirenilla — Cloud of Fear.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Cloud of Fear [Ss]pell!|Your mind is wracked by fear\\.|[\\w`' -]+ looks very afraid\\.)$")},
	// Lord Kreizenn — Wave of Flame.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You resist the Wave of Flame [Ss]pell!|You feel your skin burn\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s skin burns\\.)$")},
	// Lady Nevederia — Bellowing Winds.
	{[]string{"templeofveeshan"}, regexp.MustCompile("^(?:You begin to spin\\.|You resist the Bellowing Winds [Ss]pell!|[\\w`' -]+ begins to spin\\.)$")},

	// ── Icewell Keep ──
	// Dain Frostreaver IV — pit banish. Both shapes are Dain-specific.
	{[]string{"icewellkeep"}, regexp.MustCompile("^(?:[\\w`' -]+ is cast into the pit by Dain Frostreavers justice|The justice of Dain Frostreaver casts you into the pit)\\.$")},

	// ── Skyshrine ──
	// Lord Yelinak — Ice Breath. Landed, resist, and the third-party shape.
	{[]string{"skyshrine"}, regexp.MustCompile("^(?:You resist the Ice Breath [Ss]pell!|Shards of magical ice rend you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body is cut by shards of magical ice\\.)$")},

	// ── Western Wastes ──
	// Klandicar — Dragon Roar (fear).
	{[]string{"westernwastes"}, regexp.MustCompile(`^You (?:flee in terror\.|lose control of yourself!|resist the Dragon Roar spell!)$`)},
	// Sontalak — Lava Breath. The package shares this wording with Nagafen,
	// Ragefire, Faydedar and Telkorenar; the zone gate keeps it Sontalak's.
	{[]string{"westernwastes"}, regexp.MustCompile("^(?:You resist the Lava Breath [Ss]pell!|Your body combusts as the lava hits you\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+'s body combusts as the lava hits them\\.)$")},

	// ── The Wakening Land ──
	// Wuoshi — Dragon Roar. Identical first-person wording to Klandicar's, in
	// a different zone: this is exactly the case the zone gate exists for.
	{[]string{"wakeningland", "wakeninglands"}, regexp.MustCompile(`^You flee in terror\.$`)},

	// ── Dragon Necropolis ──
	// Zlandicar — Stun Breath.
	{[]string{"dragonnecropolis"}, regexp.MustCompile("^(?:You resist the Stun Breath [Ss]pell!|Your eardrums rupture\\.(?:  You have taken \\d+ points? of damage\\.)?|[\\w`' -]+ staggers with intense pain\\.)$")},
	// Zlandicar — Putrefy Flesh. Third-person and resist only: the package
	// leaves the first-person "Your flesh begins to liquefy." on NoTimer, and
	// this anchor keeps that judgement rather than second-guessing it.
	{[]string{"dragonnecropolis"}, regexp.MustCompile("^(?:[\\w`' -]+'s flesh begins to liquefy\\.|You resist the Putrefy Flesh [Ss]pell!)$")},

	// ── Sebilis ──
	// Trakanon — Trakanon's Touch (banish). "You have been teleported." is
	// every port in the game; only Sebilis makes it Trakanon's, and only the
	// banished player logs it at all.
	{[]string{"sebilis"}, regexp.MustCompile(`^You have been teleported\.$`)},

	// ── Plane of Growth ──
	// Tunare — a protector of growth respawn, off its death.
	{[]string{"planeofgrowth"}, regexp.MustCompile("^(?:a protector of growth has been slain by [\\w`' -]+|You have slain a protector of growth)!$")},

	// ── Plane of Sky ──
	// Sirran spawns off an island boss death and stays 15 minutes. Only the
	// killer's log carries "You have slain"; everyone else sees the broadcast.
	{[]string{"planeofsky"}, regexp.MustCompile("^(?:(?:a thunder spirit princess|Protector of Sky|Gorgalosk|Keeper of Souls|The Spiroc Lord|Bazzt Zzzt|Sister of the Spire|Eye of Veeshan) has been slain by [\\w`' -]+|You have slain (?:Thunder Spirit Princess|Protector of Sky|Gorgalosk|Keeper of Souls|The Spiroc Lord|Bazzt Zzzt|Sister of the Spire|Eye of Veeshan))!$")},
}

// raidAEForwardLine reports whether a raw log line is a raid-AE anchor worth
// forwarding: a pattern match while standing in that mob's zone. The zone
// check runs first, so for everyone outside these zones it costs a few string
// compares per line.
func raidAEForwardLine(line string) bool {
	zkey := normalizeZoneKey(CurrentZone())
	if zkey == "" {
		return false
	}
	content := ""
	for i := range raidAEForwardDefs {
		d := &raidAEForwardDefs[i]
		if !raidAEZoneMatch(d.zoneKeys, zkey) {
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

func raidAEZoneMatch(keys []string, zkey string) bool {
	for _, k := range keys {
		if k == zkey {
			return true
		}
	}
	return false
}

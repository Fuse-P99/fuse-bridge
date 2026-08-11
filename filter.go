package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	guildChatPattern   = regexp.MustCompile(`tells the guild, `)
	guildSelfPattern   = regexp.MustCompile(`You say to your guild, `)
	guildMotdPattern   = regexp.MustCompile(`GUILD MOTD:`)
	broadcastPattern   = regexp.MustCompile(`BROADCASTS, `)
	serverMsgPattern   = regexp.MustCompile(`<\[SERVER MESSAGE\]>:`)
	quakePattern       = regexp.MustCompile(`(?:You feel the (?:need to get somewhere safe quickly|sudden urge to seek a safe location)|The gods have awoken|The Gods of Norrath emit|The Gods strike all|Minions gather)`)
	engagePattern      = regexp.MustCompile(` engages \w+!`)
	slainPattern       = regexp.MustCompile(` has been slain by .+!`)
	enteredZonePattern = regexp.MustCompile(`You have entered (.+)\.`)
	// "/char" bind-point output: "You are currently bound in: <Zone>"
	boundInPattern = regexp.MustCompile(`You are currently bound in: (.+)`)
	// /loc output: "Your Location is <Y>, <X>, <Z>" (Y first, X second, Z elevation).
	locPattern = regexp.MustCompile(`Your Location is ([-\d.]+), ([-\d.]+), ([-\d.]+)`)
	// /who footer: "There are N players in <Zone>." — for a plain /who this names
	// the player's current zone. ("EverQuest" means /who all, not a real zone.)
	whoFooterZonePattern = regexp.MustCompile(`There (?:is|are) \d+ players? in (.+)\.`)
	// Matches /who output lines: header, player entries (including LINKDEAD/AFK prefixes), and footer.
	// Footer handles both "There are N players" and the single-player "There is 1 player".
	whoPattern = regexp.MustCompile(`(?:Players (?:on|in) EverQuest:|There (?:is|are) \d+ players? in|\[(?:\d+ [A-Za-z ]+|ANONYMOUS|ROLEPLAY)\])`)
	// Narandi the Wretched's zone-wide Ring War shouts — the three ocean
	// kickoff lines the server times the event's waves and attendance from.
	// Forwarded unconditionally (no settings gate): three lines per Ring War,
	// load-bearing for the guild, harmless otherwise. The server dedups the
	// copies every client in Great Divide forwards.
	ringWarShoutPattern = regexp.MustCompile(`Narandi the Wretched shouts, '`)
	// Spell resist / immune / fizzle lines. Applies to ANY resisted spell, not
	// just procs — forwarded under its own "Resist Messages" filter. Anchored to
	// the log MESSAGE (timestamp stripped first).
	// P99 prints the snare-immune line as "...changes in ITS run speed."; the
	// possessive-less form is kept for safety since both have been reported.
	resistPattern = regexp.MustCompile("^Your (?:target is immune to changes in (?:its )?run speed|spell did not take hold|target resisted the [\\w`' -]+ spell)\\.$")
	// Warrior fighting-style disciplines (Defensive / Evasive), forwarded under
	// the "Disciplines" filter so the raid card can show the main tank's
	// 3-minute window. Both shapes: other players read as "<Name> assumes", the
	// tank's own log says "You assume" — the server attributes that one to the
	// submitting character.
	tankDiscPattern     = regexp.MustCompile(`^(\w+) assumes an? (defensive|evasive) fighting style\.$`)
	tankDiscSelfPattern = regexp.MustCompile(`^You assume an? (defensive|evasive) fighting style\.$`)
	// Weapon proc-effect (landing) lines, forwarded under the "Proc Messages"
	// filter. The server counts a tank's procs from resist OR proc lines. Kept in
	// sync with the server copies (resistEffectRE/procEffectRE in eventTracker.go).
	procPattern = regexp.MustCompile("^(?:[\\w`' -]+'s (?:world dissolves into anarchy|soul is consumed by the fury of Zek)|[\\w`' -]+ (?:is (?:stunned|crushed by a wall of water|weakened by the Rage of Vallon|slowed by the mist of the seas|engulfed by a swarm of insects|deafened|blasted by a gust of bitter cold|blinded by a flash of light|surrounded by a vortex of shadows)|grimaces in pain|doubles over in pain|begins to choke|has been engulfed in the maelstrom|staggers back|has been poisone(?:d|d\\.)|sweats and shivers, looking feverish))\\.$")
)

// /who output is forwarded without a client-side rate limit. The server
// deduplicates identical /who lines (5-minute TTL), so repeated identical
// snapshots cost nothing, while distinct /who for different zones — all valuable
// for the zone roster — are no longer suppressed.

// loginTime is set whenever "Welcome to EverQuest!" appears in the log.
// A MOTD seen within loginSuppressWindow of a login is suppressed — it's the
// automatic login MOTD, not an officer update.
var loginTime time.Time

const loginSuppressWindow = 30 * time.Second

// RecordLoginLine checks the line for the login marker and updates loginTime.
// Must be called for every raw line before ShouldForward.
func RecordLoginLine(line string) {
	if strings.Contains(line, "Welcome to EverQuest!") {
		loginTime = time.Now()
		addStatus("Login detected — suppressing next MOTD")
	}
}

// ShouldForward returns true if the log line should be sent to the server,
// based on the line content and current user settings.
func ShouldForward(line string) bool {
	s := GetSettings()
	// Ring War kickoff shouts bypass the filters — see ringWarShoutPattern.
	if ringWarShoutPattern.MatchString(line) {
		return true
	}
	// Shared raid-AE anchors (Vulak / Dain / Klandicar) bypass them too —
	// zone-gated in raid_ae.go, deduped server-side. A handful of lines per
	// fight, and they restart the whole raid's cooldown bars.
	if raidAEForwardLine(line) {
		return true
	}
	//Before sending seen or entered /gu chat check the user has enabled guild chat AND does not have the bad word filter on (causes problems with dedupe).
	if s.GuildChat && (guildChatPattern.MatchString(line) || guildSelfPattern.MatchString(line)) && !badWordFilter {
		return true
	}
	if s.GuildMotd && guildMotdPattern.MatchString(line) {
		// Suppress the automatic MOTD shown on every login.
		if time.Since(loginTime) < loginSuppressWindow {
			return false
		}
		//Setting this to never return for now. Right now anyone can run /get to see the motd and I can't differentiate between that and this being set by an officer. When Fuselog is online they will see the new get message and it will be sent from them.
		// return true
	}
	if s.Broadcasts && broadcastPattern.MatchString(line) {
		return true
	}
	if s.ServerMessages && serverMsgPattern.MatchString(line) {
		return true
	}
	if s.QuakeMessages && quakePattern.MatchString(line) {
		return true
	}
	if s.EngageMessages && engagePattern.MatchString(line) {
		return true
	}
	if s.WhoOutput && whoPattern.MatchString(line) {
		return true
	}
	if s.CharacterLocations && enteredZonePattern.MatchString(line) {
		return true
	}
	if s.BindLocation && boundInPattern.MatchString(line) {
		return true
	}
	// Forward ALL slain messages (not just raid mobs): the server uses them for
	// raid-mob ToDs, to clear a dead current add mob, and to grey out CH-chain
	// clerics who die. Non-raid victims are harmlessly ignored server-side.
	if s.SlainMessages && slainPattern.MatchString(line) {
		return true
	}
	// "Your spell is interrupted." — the server stops the sending cleric's CH
	// cast bar on the raid card, letting out-of-game viewers see the interrupt.
	// Defaults on; cheap exact match after the timestamp strip.
	if s.InterruptMessages && logMessageContent(line) == "Your spell is interrupted." {
		return true
	}
	// Resist / proc-effect combat lines, each under its own opt-in filter (Proc
	// Messages auto-enables Resist Messages in the UI). The server only counts
	// these toward a tank's proc total, so non-tank forwards are discarded there.
	if s.ResistMessages || s.ProcMessages {
		content := logMessageContent(line)
		if s.ResistMessages && resistPattern.MatchString(content) {
			return true
		}
		if s.ProcMessages && procPattern.MatchString(content) {
			return true
		}
	}
	// Fighting-style disciplines. Everyone in range sees the tank's line, so the
	// server gets several copies and collapses them; forwarding from every client
	// is what lets out-of-game viewers see the window at all.
	if s.DisciplineMessages {
		content := logMessageContent(line)
		if tankDiscPattern.MatchString(content) || tankDiscSelfPattern.MatchString(content) {
			return true
		}
	}
	return false
}

// logMessageContent strips the "[timestamp] " prefix from a log line, returning
// just the message (what the anchored proc regex expects).
func logMessageContent(line string) string {
	if i := strings.Index(line, "] "); i >= 0 {
		return strings.TrimSpace(line[i+2:])
	}
	return strings.TrimSpace(line)
}

// IsZoneLoadingLine reports whether a line is EQ's zone-load banner
// ("LOADING, PLEASE WAIT..."). It marks the start of the gap that ends with
// "You have entered <zone>." — during which the character is being handed
// between zone servers and buff durations don't advance. See the zone-load
// freeze in triggers_engine.go.
//
// Matched as a prefix: the trailing ellipsis has varied between clients, and
// the message is distinctive enough on its own.
func IsZoneLoadingLine(line string) bool {
	return strings.HasPrefix(logMessageContent(line), "LOADING, PLEASE WAIT")
}

// ExtractZone returns the zone name from a "You have entered X." line, or "".
//
// EQ reuses that exact wording for something that is not a zone at all:
// "You have entered an area where levitation does not function." It fires on
// crossing an interior boundary with levitate up, names no zone, and is
// indistinguishable to the pattern — so without this guard it would set the
// player's current zone to that sentence. Everything keyed off the current zone
// then quietly breaks: the raid overlays' in-zone gate stops matching, and the
// server records the toon as standing somewhere that doesn't exist.
func ExtractZone(line string) string {
	m := enteredZonePattern.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(m[1]), "an area ") {
		return ""
	}
	return m[1]
}

// ExtractBind returns the bind zone from a "/char" line ("You are currently
// bound in: X"), stripped of any trailing period, or "" if the line isn't one.
func ExtractBind(line string) string {
	m := boundInPattern.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(m[1]), ".")
}

// ExtractLoc parses a "/loc" line ("Your Location is Y, X, Z") and returns the
// EQ world coordinates (y, x, z) and ok=true on a match. Note the log order is
// Y, X, Z; the returned values are reordered to (y, x, z).
func ExtractLoc(line string) (y, x, z float64, ok bool) {
	m := locPattern.FindStringSubmatch(line)
	if len(m) < 4 {
		return 0, 0, 0, false
	}
	y = parseFloat(m[1])
	x = parseFloat(m[2])
	z = parseFloat(m[3])
	return y, x, z, true
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// ExtractWhoZone returns the current zone from a /who footer line ("There are N
// players in <Zone>."), or "" if the line isn't a footer or names "EverQuest"
// (which indicates /who all rather than a single-zone /who).
func ExtractWhoZone(line string) string {
	m := whoFooterZonePattern.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	zone := strings.TrimSpace(m[1])
	if strings.EqualFold(zone, "EverQuest") {
		return ""
	}
	return zone
}

// rewriteSelfGuildSay converts the player's own guild-say format into the
// third-person format the server expects.
// "[...] You say to your guild, 'hi'" → "[...] Charactername tells the guild, 'hi'"
func rewriteSelfGuildSay(line string) string {
	if !guildSelfPattern.MatchString(line) {
		return line
	}
	name := currentCharName
	if name == "" {
		return line
	}
	return strings.Replace(line, "You say to your guild, ", name+" tells the guild, ", 1)
}

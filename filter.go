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
	// /loc output: "Your Location is <Y>, <X>, <Z>" (Y first, X second, Z elevation).
	locPattern = regexp.MustCompile(`Your Location is ([-\d.]+), ([-\d.]+), ([-\d.]+)`)
	// /who footer: "There are N players in <Zone>." — for a plain /who this names
	// the player's current zone. ("EverQuest" means /who all, not a real zone.)
	whoFooterZonePattern = regexp.MustCompile(`There (?:is|are) \d+ players? in (.+)\.`)
	// Matches /who output lines: header, player entries (including LINKDEAD/AFK prefixes), and footer.
	// Footer handles both "There are N players" and the single-player "There is 1 player".
	whoPattern = regexp.MustCompile(`(?:Players (?:on|in) EverQuest:|There (?:is|are) \d+ players? in|\[(?:\d+ [A-Za-z ]+|ANONYMOUS|ROLEPLAY)\])`)
	// Spell resist / immune / fizzle lines. Applies to ANY resisted spell, not
	// just procs — forwarded under its own "Resist Messages" filter. Anchored to
	// the log MESSAGE (timestamp stripped first).
	resistPattern = regexp.MustCompile("^Your (?:target is immune to changes in run speed|spell did not take hold|target resisted the [\\w`' -]+ spell)\\.$")
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
	// Forward ALL slain messages (not just raid mobs): the server uses them for
	// raid-mob ToDs, to clear a dead current add mob, and to grey out CH-chain
	// clerics who die. Non-raid victims are harmlessly ignored server-side.
	if s.SlainMessages && slainPattern.MatchString(line) {
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

// ExtractZone returns the zone name from a "You have entered X." line, or "".
func ExtractZone(line string) string {
	m := enteredZonePattern.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return m[1]
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

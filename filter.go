package main

import (
	"regexp"
	"strings"
	"sync"
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
	slainPattern      = regexp.MustCompile(` has been slain by .+!`)
	slainMobExtractRE = regexp.MustCompile(`(?:\t|] )(.+?) has been slain by`)
	enteredZonePattern = regexp.MustCompile(`You have entered (.+)\.`)
	// Matches /who output lines: header, player entries (including LINKDEAD/AFK prefixes), and footer.
	whoPattern = regexp.MustCompile(`(?:Players (?:on|in) EverQuest:|There are \d+ players in|\[(?:\d+ [A-Za-z ]+|ANONYMOUS)\])`)
)

var (
	whoHeaderRE = regexp.MustCompile(`Players (?:on|in) EverQuest:`)
	whoFooterRE = regexp.MustCompile(`There are \d+ players in`)

	whoRateMu        sync.Mutex
	whoLastForwarded time.Time
	whoSuppressing   bool
)

const whoRateLimit = 30 * time.Second

// shouldForwardWhoLine enforces the 30-second rate limit on /who output.
// It tracks block state so the entire block (header + players + footer) is
// either forwarded or suppressed as a unit.
func shouldForwardWhoLine(line string) bool {
	whoRateMu.Lock()
	defer whoRateMu.Unlock()

	if whoHeaderRE.MatchString(line) {
		if time.Since(whoLastForwarded) < whoRateLimit {
			whoSuppressing = true
			return false
		}
		whoLastForwarded = time.Now()
		whoSuppressing = false
		return true
	}

	if whoSuppressing {
		if whoFooterRE.MatchString(line) {
			whoSuppressing = false
		}
		return false
	}

	return true
}

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
	if s.GuildChat && (guildChatPattern.MatchString(line) || guildSelfPattern.MatchString(line)) {
		return true
	}
	if s.GuildMotd && guildMotdPattern.MatchString(line) {
		// Suppress the automatic MOTD shown on every login.
		if time.Since(loginTime) < loginSuppressWindow {
			return false
		}
		return true
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
		return shouldForwardWhoLine(line)
	}
	if s.CharacterLocations && enteredZonePattern.MatchString(line) {
		return true
	}
	if s.SlainMessages && isRaidMobSlain(line) {
		return true
	}
	return false
}

// isRaidMobSlain returns true when the slain mob name matches a mob flagged
// as a raid mob in the locally cached list fetched from the server.
func isRaidMobSlain(line string) bool {
	if !slainPattern.MatchString(line) {
		return false
	}
	m := slainMobExtractRE.FindStringSubmatch(line)
	if len(m) < 2 {
		return false
	}
	return IsRaidMob(m[1])
}

// ExtractZone returns the zone name from a "You have entered X." line, or "".
func ExtractZone(line string) string {
	m := enteredZonePattern.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return m[1]
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

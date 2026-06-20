package main

import (
	"regexp"
	"strings"
	"time"
)

var (
	guildChatPattern  = regexp.MustCompile(`tells the guild, `)
	guildSelfPattern  = regexp.MustCompile(`You say to your guild, `)
	guildMotdPattern  = regexp.MustCompile(`GUILD MOTD:`)
	broadcastPattern  = regexp.MustCompile(`BROADCASTS, `)
	serverMsgPattern  = regexp.MustCompile(`<\[SERVER MESSAGE\]>:`)
	quakePattern      = regexp.MustCompile(`(?:You feel the (?:need to get somewhere safe quickly|sudden urge to seek a safe location)|The gods have awoken|The Gods of Norrath emit|The Gods strike all|Minions gather)`)
	engagePattern     = regexp.MustCompile(` engages \w+!`)
	// Matches /who output lines: header, player entries, and footer.
	// Uses "] [" (timestamp-close + player-bracket) to avoid false positives.
	whoPattern        = regexp.MustCompile(`(?:Players (?:on|in) EverQuest:|There are \d+ players in|\] \[(?:\d+ [A-Za-z]|ANONYMOUS)\] \w)`)
)

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
		return true
	}
	return false
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

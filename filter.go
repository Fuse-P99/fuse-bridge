package main

import (
	"regexp"
	"strings"
)

var (
	guildChatPattern  = regexp.MustCompile(`tells the guild, `)
	guildSelfPattern  = regexp.MustCompile(`You say to your guild, `)
	guildMotdPattern  = regexp.MustCompile(`GUILD MOTD:`)
	broadcastPattern  = regexp.MustCompile(`BROADCASTS, `)
	serverMsgPattern  = regexp.MustCompile(`<\[SERVER MESSAGE\]>:`)
	quakePattern      = regexp.MustCompile(`(?:You feel the (?:need to get somewhere safe quickly|sudden urge to seek a safe location)|The gods have awoken|The Gods of Norrath emit|The Gods strike all|Minions gather)`)
)

// ShouldForward returns true if the log line should be sent to the server,
// based on the line content and current user settings.
func ShouldForward(line string) bool {
	s := GetSettings()
	if s.GuildChat && (guildChatPattern.MatchString(line) || guildSelfPattern.MatchString(line)) {
		return true
	}
	if s.GuildMotd && guildMotdPattern.MatchString(line) {
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

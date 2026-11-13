package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Guild-membership watcher. The roster table on the server can say a
// character is Fuse for hours (or, before the removal cascade, forever) after
// they've actually joined another guild — long enough for that guild's /gu to
// relay into #guild-stream. The client, though, SEES the join happen in its
// own log:
//
//	[Tue Jul 07 11:43:05 2026] You have joined Fuse.
//	[Tue Jul 07 11:43:05 2026] You are now a regular member of the guild.
//
// Both lines print together on any guild join. When the joined guild is not
// Fuse, the character is flagged (persistently, per (character, world) storage
// key) and every submission carries the guild's name — the server diverts that
// character's guild chat to the other-guild intel feed instead of the stream.
// Joining Fuse clears the flag. This is NOT a punishment or a revocation:
// members may park characters in a friend's guild and remain raiders in good
// standing; only where that character's guild chat flows changes.
//
// The confirmation line is required because "You have joined <name>." alone is
// ambiguous — chat-channel joins print the same shape — and a false positive
// here would silently divert a real member's guild chat.
var (
	gwJoinedRE = regexp.MustCompile(`^You have joined (.+)\.$`)
	gwMemberRE = regexp.MustCompile(`^You are now an? \w+ member of the guild\.$`)
)

// gwPairWindow is how long a "You have joined X." line waits for its
// "member of the guild" confirmation (they print within the same second).
const gwPairWindow = 5 * time.Second

var (
	gwMu     sync.Mutex
	gwLoaded bool
	gwGuilds = map[string]string{} // charKey (storeKeyFor) → non-Fuse guild name
	// Pending pair state: the guild named by the most recent join line.
	gwPending string
	gwPendAt  time.Time
)

func gwPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "other_guilds.json")
}

// gwEnsureLoadedLocked lazily loads the persisted flags. Caller holds gwMu.
func gwEnsureLoadedLocked() {
	if gwLoaded {
		return
	}
	gwLoaded = true
	data, err := os.ReadFile(gwPath())
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(data, &m) == nil && m != nil {
		gwGuilds = m
	}
}

// gwSaveLocked persists the flags. Caller holds gwMu.
func gwSaveLocked() {
	data, err := json.MarshalIndent(gwGuilds, "", "  ")
	if err != nil {
		return
	}
	path := gwPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// RecordGuildJoinLine watches the tailed log for the guild-join pair and keeps
// the tailed character's other-guild flag current. Called for every raw line
// (main.go filter loop).
func RecordGuildJoinLine(line string) {
	content := logMessageContent(line)
	if content == "" {
		return
	}
	if m := gwJoinedRE.FindStringSubmatch(content); m != nil {
		gwMu.Lock()
		gwPending, gwPendAt = strings.TrimSpace(m[1]), time.Now()
		gwMu.Unlock()
		return
	}
	if !gwMemberRE.MatchString(content) {
		return
	}
	gwMu.Lock()
	guild := gwPending
	confirmed := guild != "" && time.Since(gwPendAt) <= gwPairWindow
	gwPending = ""
	gwMu.Unlock()
	if !confirmed || currentCharKey == "" {
		return
	}
	if strings.EqualFold(guild, "Fuse") {
		clearCharOtherGuild(currentCharKey)
	} else {
		setCharOtherGuild(currentCharKey, guild)
	}
}

// setCharOtherGuild flags a character as belonging to a non-Fuse guild.
func setCharOtherGuild(charKey, guild string) {
	gwMu.Lock()
	gwEnsureLoadedLocked()
	if strings.EqualFold(gwGuilds[charKey], guild) {
		gwMu.Unlock()
		return
	}
	gwGuilds[charKey] = guild
	gwSaveLocked()
	gwMu.Unlock()
	addStatus("%s joined the guild %q — this character's guild chat will not stream to Fuse (captured as other-guild intel instead).",
		charKeyDisplay(charKey), guild)
}

// clearCharOtherGuild removes the flag (the character joined Fuse).
func clearCharOtherGuild(charKey string) {
	gwMu.Lock()
	gwEnsureLoadedLocked()
	if _, flagged := gwGuilds[charKey]; !flagged {
		gwMu.Unlock()
		return
	}
	delete(gwGuilds, charKey)
	gwSaveLocked()
	gwMu.Unlock()
	addStatus("%s joined Fuse — guild chat streaming restored.", charKeyDisplay(charKey))
}

// CharOtherGuild returns the non-Fuse guild this character is known to be in,
// or "" (in Fuse, in no guild, or never observed joining one). Read by the
// sender (tags submissions) and the forwarding filter (suppresses the other
// guild's MOTD).
func CharOtherGuild(charKey string) string {
	if charKey == "" {
		return ""
	}
	gwMu.Lock()
	defer gwMu.Unlock()
	gwEnsureLoadedLocked()
	return gwGuilds[charKey]
}

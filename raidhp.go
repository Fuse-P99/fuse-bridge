package main

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live target-HP tracking: the client tails its own EQ log, so it can update mob
// health bars in real time from guild-chat HP calls — without waiting for the
// next /timers poll. Matching is strict: the call must contain a watched mob's
// EXACT name, a 1-3 digit number immediately followed by '%' (space allowed),
// and nothing else besides punctuation — "Zlandicar - 45 %", "Zlandicar, 45%",
// "45% Zlandicar". We watch every currently-popped mob (set from the timers
// payload), and drop a mob to 0% when we see it slain or a !tod for it.

type mobHPEntry struct {
	pct int
	at  time.Time
}

var (
	hpMu      sync.Mutex
	watchMobs = map[string][]string{}     // lower mob name → distinctive words
	mobHP     = map[string]mobHPEntry{}   // lower mob name → last-seen HP
)

var (
	guildChatHPRE = regexp.MustCompile(`(?:tells the guild, '|say to your guild, ')(.*)'`)
	hpPctRE       = regexp.MustCompile(`(\d{1,3})\s*%`)
	hpAlnumRE     = regexp.MustCompile(`[a-z0-9]`)
)

func mobWords(name string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(name)) {
		if len(w) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

// SetWatchedMobs replaces the set of popped mob names whose HP calls we track.
func SetWatchedMobs(names []string) {
	hpMu.Lock()
	defer hpMu.Unlock()
	watchMobs = make(map[string][]string, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		watchMobs[strings.ToLower(n)] = mobWords(n)
	}
}

// matchWatchedMobLocked returns the watched mob name whose words appear in text.
func matchWatchedMobLocked(lowerText string) string {
	best := ""
	bestLen := 0
	for name, words := range watchMobs {
		for _, w := range words {
			if len(w) > bestLen && strings.Contains(lowerText, w) {
				best = name
				bestLen = len(w)
			}
		}
	}
	return best
}

// RecordRaidHPFromLine updates live HP from a guild-chat HP call, or drops a mob
// to 0% when it's slain or a !tod is issued for it.
func RecordRaidHPFromLine(line string) {
	lower := strings.ToLower(line)

	// Death → 0%.
	if strings.Contains(lower, "has been slain by") || strings.Contains(lower, "!tod") {
		hpMu.Lock()
		if name := matchWatchedMobLocked(lower); name != "" {
			mobHP[name] = mobHPEntry{pct: 0, at: time.Now()}
		}
		hpMu.Unlock()
		return
	}

	// Guild-chat HP call — strict: exact watched-mob name + one 1-3 digit
	// number immediately followed by '%' (space allowed), with nothing else
	// besides punctuation. Anything looser kept scraping numbers out of
	// unrelated chatter that happened to mention a popped mob.
	m := guildChatHPRE.FindStringSubmatch(line)
	if m == nil {
		return
	}
	content := strings.ToLower(m[1])
	hpMu.Lock()
	defer hpMu.Unlock()
	for name := range watchMobs {
		idx := strings.Index(content, name)
		if idx < 0 {
			continue
		}
		rest := content[:idx] + content[idx+len(name):]
		mm := hpPctRE.FindStringSubmatch(rest)
		if mm == nil {
			continue // no %-suffixed number — bare numbers no longer count
		}
		n, err := strconv.Atoi(mm[1])
		if err != nil || n < 0 || n > 100 {
			continue
		}
		if hpAlnumRE.MatchString(strings.Replace(rest, mm[0], "", 1)) {
			continue // extra words/numbers besides name+percent — not an HP call
		}
		mobHP[name] = mobHPEntry{pct: n, at: time.Now()}
		return
	}
}

// GetMobHPs returns lower-mob-name → HP percent for fresh (<10 min) entries.
func GetMobHPs() map[string]int {
	hpMu.Lock()
	defer hpMu.Unlock()
	out := make(map[string]int)
	cutoff := time.Now().Add(-10 * time.Minute)
	for name, e := range mobHP {
		if e.at.After(cutoff) {
			out[name] = e.pct
		}
	}
	return out
}

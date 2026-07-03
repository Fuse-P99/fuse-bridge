package main

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live target-HP tracking: the client tails its own EQ log, so it can update mob
// health bars in real time from guild-chat calls like "Zlandicar - 45%",
// "Zlandicar 45", or "45% Zlandicar" — without waiting for the next /timers poll.
// We watch every currently-popped mob (set from the timers payload), and drop a
// mob to 0% when we see it slain or a !tod for it.

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
	hpNumRE       = regexp.MustCompile(`\b(\d{1,3})\b`)
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

	// Guild-chat HP call.
	m := guildChatHPRE.FindStringSubmatch(line)
	if m == nil {
		return
	}
	content := m[1]
	hpMu.Lock()
	defer hpMu.Unlock()
	name := matchWatchedMobLocked(strings.ToLower(content))
	if name == "" {
		return
	}
	var pctStr string
	if mm := hpPctRE.FindStringSubmatch(content); mm != nil {
		pctStr = mm[1]
	} else if mm := hpNumRE.FindStringSubmatch(content); mm != nil {
		pctStr = mm[1]
	} else {
		return
	}
	n, err := strconv.Atoi(pctStr)
	if err != nil || n < 0 || n > 100 {
		return
	}
	mobHP[name] = mobHPEntry{pct: n, at: time.Now()}
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

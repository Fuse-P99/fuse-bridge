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
// payload), and drop a mob to 0% when we see it slain or a !tod for it — by
// the VICTIM's name in the slain line, never the killer's: "Dooce has been
// slain by Lord Koi`Doken!" names the boss as the killer, and matching that
// zeroed the boss's bar on every raider death until the next HP call (the
// "callout, then 0%" report). Name parts that are mere titles ("Lord",
// "Lady") never identify a mob on their own, so a "Lord Feshlak" kill can't
// stand in for Lord Koi`Doken either.

type mobHPEntry struct {
	pct int
	at  time.Time
}

var (
	hpMu      sync.Mutex
	watchMobs = map[string][]string{}   // lower mob name → distinctive words
	mobHP     = map[string]mobHPEntry{} // lower mob name → last-seen HP
)

var (
	guildChatHPRE = regexp.MustCompile(`(?:tells the guild, '|say to your guild, ')(.*)'`)
	hpPctRE       = regexp.MustCompile(`(\d{1,3})\s*%`)
	hpAlnumRE     = regexp.MustCompile(`[a-z0-9]`)
)

// canonTicks makes apostrophe and backtick interchangeable in mob names
// ("Vulak`Aerr" is routinely typed "Vulak'Aerr"). Single-byte swap, so string
// lengths and offsets are preserved.
func canonTicks(s string) string { return strings.ReplaceAll(s, "'", "`") }

// hpTitleWords are name parts too common to identify a mob by themselves.
var hpTitleWords = map[string]bool{
	"lord": true, "lady": true, "king": true, "queen": true, "prince": true,
	"princess": true, "sister": true, "keeper": true, "protector": true,
	"master": true, "guardian": true, "sentinel": true, "servant": true,
	"warder": true, "champion": true,
}

// mobWords returns the parts of a mob's name distinctive enough to identify
// it in a !tod ("!tod vulak`aerr"): 4+ characters and not a bare title.
func mobWords(name string) []string {
	var out []string
	for _, w := range strings.Fields(canonTicks(strings.ToLower(name))) {
		if len(w) >= 4 && !hpTitleWords[w] {
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

// matchWatchedNameLocked returns the watched mob that text (lowercased, ticks
// canonical) names: its full name if present, else its longest distinctive
// word. The longest claim wins when several watched mobs could match.
func matchWatchedNameLocked(text string) string {
	best := ""
	bestLen := 0
	for name, words := range watchMobs {
		if cn := canonTicks(name); strings.Contains(text, cn) {
			if len(cn) > bestLen {
				best = name
				bestLen = len(cn)
			}
			continue
		}
		for _, w := range words {
			if len(w) > bestLen && strings.Contains(text, w) {
				best = name
				bestLen = len(w)
			}
		}
	}
	return best
}

// slainVictim returns what died in a slain line, lowercased: the text before
// " has been slain by " (after the log timestamp), or the object of the
// first-person "You have slain X!". ok is false for any other line.
func slainVictim(lower string) (victim string, ok bool) {
	msg := lower
	if i := strings.Index(msg, "] "); i >= 0 {
		msg = msg[i+2:]
	}
	if i := strings.Index(msg, " has been slain by "); i >= 0 {
		return strings.TrimSpace(msg[:i]), true
	}
	const you = "you have slain "
	if strings.HasPrefix(msg, you) {
		return strings.TrimSuffix(strings.TrimSpace(msg[len(you):]), "!"), true
	}
	return "", false
}

// RecordRaidHPFromLine updates live HP from a guild-chat HP call, or drops a mob
// to 0% when it's slain or a !tod is issued for it.
func RecordRaidHPFromLine(line string) {
	// The watched set is BLUE raid mobs (the board is visible to linked members
	// on any world) — but HP evidence must come from the Blue log: Green has a
	// Trakanon too, and a Green kill must not zero the Blue card's bar.
	if !onHomeServer() {
		return
	}
	lower := strings.ToLower(line)

	// Death → 0%. Only the slain line's VICTIM, or the text after a !tod, may
	// name the mob (see the file comment): the killer's name is never read.
	if victim, ok := slainVictim(lower); ok {
		hpMu.Lock()
		if name := matchWatchedNameLocked(canonTicks(victim)); name != "" {
			mobHP[name] = mobHPEntry{pct: 0, at: time.Now()}
		}
		hpMu.Unlock()
		return
	}
	if i := strings.Index(lower, "!tod"); i >= 0 {
		hpMu.Lock()
		if name := matchWatchedNameLocked(canonTicks(lower[i+len("!tod"):])); name != "" {
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
	content := canonTicks(strings.ToLower(m[1]))
	hpMu.Lock()
	defer hpMu.Unlock()
	for name := range watchMobs {
		cn := canonTicks(name)
		idx := strings.Index(content, cn)
		if idx < 0 {
			continue
		}
		rest := content[:idx] + content[idx+len(cn):]
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

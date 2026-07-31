package main

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// Local raid timer tracking: the client tails its own EQ log, so debuff-macro
// and CH-chain guild calls can start timer bars on the raid card the instant
// they're seen — no server round trip. The server card carries the same
// timestamps as a fallback so people who are out of game still get bars
// (theirs start when the server saw the call instead).
//
// Debuff bar lengths come from the Fuse Triggers package: the debuff-macro
// call groups ("Debuff Macros" and its "(Public)" sibling). Each tracked
// debuff keyword uses the countdown duration configured on that group's
// matching trigger. CH bars are the fixed 10s Complete Heal cast:
// re-calling the same slot restarts the bar, and the caster's own "Your spell
// is interrupted." line stops it (other viewers learn of the interrupt through
// the server, via the Spell Interrupts forwarding filter).

const (
	// localRaidTimerTTL prunes local entries long after any bar they could draw.
	localRaidTimerTTL = 10 * time.Minute
	// debuffDursRefresh re-scans the Fuse package for durations even without a
	// version change (a local officer edit doesn't bump fuseVersion).
	debuffDursRefresh = 5 * time.Minute
)

// LocalDebuffTimer is one locally-seen debuff macro call.
type LocalDebuffTimer struct {
	Name   string `json:"name"`   // lower debuff key: tash/malo/oos/slow/eslow/cripple
	Target string `json:"target"` // target text as typed in the call
	AtMs   int64  `json:"at_ms"`
	DurMs  int64  `json:"dur_ms"` // 0 = no configured trigger duration (no bar)
}

// LocalCHTimer is one locally-seen CH chain call.
type LocalCHTimer struct {
	Label           string `json:"label"` // slot label as called: "111", "000", "AAA", "RR1"
	Cleric          string `json:"cleric"`
	AtMs            int64  `json:"at_ms"`
	InterruptedAtMs int64  `json:"interrupted_at_ms"` // own interrupted cast only
}

// LocalRaidTimers is the payload GetLocalRaidTimers returns to the frontend.
type LocalRaidTimers struct {
	Debuffs []LocalDebuffTimer `json:"debuffs"`
	CH      []LocalCHTimer     `json:"ch"`
	// DebuffDurations maps a debuff key to its configured trigger duration in
	// ms, so bars can also be drawn for server-reported (fallback) casts.
	DebuffDurations map[string]int64 `json:"debuff_durations"`
}

var (
	raidTimersMu  sync.Mutex
	localDebuffs  = map[string]*LocalDebuffTimer{} // name|normalized target → timer
	localCH       = map[string]*LocalCHTimer{}     // label|lower cleric → timer
	debuffDurs    map[string]int64                 // lower debuff key → duration ms
	debuffDursVer int                              // fuseVersion the durations came from
	debuffDursAt  time.Time                        // when they were built
)

var (
	// Guild chat as it appears in the player's own log. Self lines keep their
	// "You say to your guild" form here (rewriteSelfGuildSay only runs on the
	// forwarding path), so both shapes are handled.
	rtGuildRE = regexp.MustCompile(`^(\w+) tells the guild, '(.*)'$`)
	rtSelfRE  = regexp.MustCompile(`^You say to your guild, '(.*)'$`)
	// Debuff macro content — the same keyword set the server tracks. Anchored,
	// so ESLOW can never half-match SLOW.
	rtDebuffRE = regexp.MustCompile(`(?i)^(TASH|MALO|OOS|ESLOW|SLOW|CRIPPLE)\s*-\s*(.+)$`)
	// CH chain content, mirrored from the server's reCHChain/reCHChainRampage
	// so local bars key to the same slot labels the card shows.
	rtCHRE     = regexp.MustCompile(`^([0-9]{3}|[A-Z]{3})\s*-\s*CH\s*-\s*(.+)$`)
	rtCHRampRE = regexp.MustCompile(`(?i)^RR(\d+)\s*-\s*CH\s*-\s*(.+)$`)
)

// rtNormName lowercases and strips non-alphanumerics for loose target matching
// ("Vessel Drozlin" ↔ "vessel", "Vulak`Aerr" ↔ "vulakaerr").
var rtNonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

func rtNormName(s string) string {
	return rtNonAlnumRE.ReplaceAllString(strings.ToLower(s), "")
}

// RecordRaidTimerLine feeds one raw log line into the local raid-timer state.
// Called for every tailed line (cheap: two anchored regex probes at most).
func RecordRaidTimerLine(line string) {
	msg := logMessageContent(line)

	// Own interrupted cast → stop this character's CH bars locally.
	if msg == "Your spell is interrupted." {
		me := strings.ToLower(strings.TrimSpace(currentCharName))
		if me == "" {
			return
		}
		now := time.Now().UnixMilli()
		raidTimersMu.Lock()
		for _, t := range localCH {
			if strings.ToLower(t.Cleric) == me {
				t.InterruptedAtMs = now
			}
		}
		raidTimersMu.Unlock()
		return
	}

	sender, content, ok := guildSenderContent(msg)
	if !ok {
		return
	}

	if m := rtDebuffRE.FindStringSubmatch(content); len(m) > 2 {
		name := strings.ToLower(m[1])
		target := strings.TrimSpace(m[2])
		durs := debuffDurations()
		raidTimersMu.Lock()
		localDebuffs[name+"|"+rtNormName(target)] = &LocalDebuffTimer{
			Name: name, Target: target, AtMs: time.Now().UnixMilli(), DurMs: durs[name],
		}
		raidTimersMu.Unlock()
		return
	}

	if m := rtCHRE.FindStringSubmatch(content); len(m) > 2 {
		recordLocalCH(m[1], sender)
		return
	}
	if m := rtCHRampRE.FindStringSubmatch(content); len(m) > 2 {
		recordLocalCH("RR"+m[1], sender)
	}
}

// guildSenderContent splits a guild-chat log message into (sender, content).
// Self lines report the tailed character as the sender.
func guildSenderContent(msg string) (sender, content string, ok bool) {
	if m := rtSelfRE.FindStringSubmatch(msg); len(m) > 1 {
		return currentCharName, m[1], true
	}
	if m := rtGuildRE.FindStringSubmatch(msg); len(m) > 2 {
		return m[1], m[2], true
	}
	return "", "", false
}

// recordLocalCH starts (or restarts) the 10s CH bar for one slot+cleric. A
// repeat of the same assignment overwrites the entry, which both restarts the
// bar and clears any earlier interrupt.
func recordLocalCH(label, cleric string) {
	cleric = strings.TrimSpace(cleric)
	if cleric == "" {
		return
	}
	raidTimersMu.Lock()
	localCH[label+"|"+strings.ToLower(cleric)] = &LocalCHTimer{
		Label: label, Cleric: cleric, AtMs: time.Now().UnixMilli(),
	}
	raidTimersMu.Unlock()
}

// GetLocalRaidTimersData snapshots the local timer state (pruning stale
// entries) plus the current debuff duration table.
func GetLocalRaidTimersData() LocalRaidTimers {
	out := LocalRaidTimers{DebuffDurations: debuffDurations()}
	cutoff := time.Now().Add(-localRaidTimerTTL).UnixMilli()
	raidTimersMu.Lock()
	for k, d := range localDebuffs {
		if d.AtMs < cutoff {
			delete(localDebuffs, k)
			continue
		}
		out.Debuffs = append(out.Debuffs, *d)
	}
	for k, c := range localCH {
		if c.AtMs < cutoff {
			delete(localCH, k)
			continue
		}
		out.CH = append(out.CH, *c)
	}
	raidTimersMu.Unlock()
	return out
}

// debuffKeys in match order — "eslow" before "slow" so an ESlow trigger can't
// be claimed by the Slow key.
var debuffKeys = []string{"eslow", "tash", "malo", "oos", "slow", "cripple"}

// debuffDurations returns the lower-debuff-key → duration-ms table from the
// Fuse package's "Debuffs Macros" triggers, cached until the Fuse set version
// changes (or a periodic refresh, to pick up local officer edits).
func debuffDurations() map[string]int64 {
	raidTimersMu.Lock()
	cached, cachedVer := debuffDurs, debuffDursVer
	fresh := cached != nil && time.Since(debuffDursAt) < debuffDursRefresh
	raidTimersMu.Unlock()

	trigStoreMu.Lock()
	ver := fuseVersion
	if fresh && cachedVer == ver {
		trigStoreMu.Unlock()
		return cached
	}
	m := map[string]int64{}
	scanDebuffGroupLocked(fuseRoot, false, m)
	trigStoreMu.Unlock()

	raidTimersMu.Lock()
	debuffDurs, debuffDursVer, debuffDursAt = m, ver, time.Now()
	raidTimersMu.Unlock()
	return m
}

// isDebuffMacroGroup reports whether a group holds the guild debuff macro call
// triggers. Matched loosely — "Debuff Macros", "Debuffs Macros", "Debuff
// Macros (Public)" all qualify. The exact-name match this replaced looked for
// "Debuffs Macros" while the live package spells it "Debuff Macros", which
// silently produced an empty duration table and therefore no debuff bars.
func isDebuffMacroGroup(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "debuff") && strings.Contains(n, "macro")
}

// scanDebuffGroupLocked walks the trigger tree; once inside a debuff-macro
// group it records each timered trigger's duration under the debuff keyword
// found in its name/pattern. Caller holds trigStoreMu.
func scanDebuffGroupLocked(g *GinaGroup, inDebuffGroup bool, out map[string]int64) {
	if g == nil {
		return
	}
	in := inDebuffGroup || isDebuffMacroGroup(g.Name)
	if in {
		for _, t := range g.Triggers {
			addDebuffDuration(t, out)
		}
	}
	for _, sub := range g.Groups {
		scanDebuffGroupLocked(sub, in, out)
	}
}

// addDebuffDuration maps one Debuffs Macros trigger to its debuff keyword and
// timer duration (mirroring the engine's duration precedence).
func addDebuffDuration(t *GinaTrigger, out map[string]int64) {
	if t == nil || t.TimerType != "Timer" {
		return
	}
	durMs := t.TimerMillisecondDuration
	if durMs <= 0 {
		durMs = int64(t.TimerDuration) * 1000
	}
	if durMs <= 0 {
		return
	}
	hay := strings.ToLower(t.Name + " " + t.TriggerText + " " + t.TimerName)
	for _, k := range debuffKeys {
		// "Malosini (MALOSINI)" contains "malo" but is its own spell — it must
		// not claim the Malo bar's duration.
		if k == "malo" && strings.Contains(hay, "malosini") {
			continue
		}
		if strings.Contains(hay, k) {
			if _, dup := out[k]; !dup {
				out[k] = durMs
			}
			return
		}
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
)

// The trigger engine matches every tailed log line against the active set of
// GINA triggers (the current character's enabled groups) and produces:
//   - a single "latest alert" (UseText → DisplayText),
//   - countdown timers (TimerType=Timer → TimerName/duration),
//   - an activity feed of fired triggers.
//
// GINA is a .NET app, so ~5% of patterns use .NET-only regex (lookarounds,
// (?<name>) groups). We compile with the stdlib RE2 engine first and fall back
// to regexp2 (a Go port of the .NET engine, with a match timeout since it can
// backtrack). Patterns neither engine accepts are skipped and surfaced as
// "unsupported" in the edit view. GINA matches case-insensitively.

// trigDef is a self-contained copy of the fields the engine needs, snapshotted
// at activation-build time so matching never touches the (mutable) store tree.
type trigDef struct {
	id            int
	path          []string // ancestor group names (trigger name kept separately)
	name          string
	pattern       string
	enableRegex   bool
	useText       bool
	displayText   string
	timer         bool
	timerName     string
	durMs         int64
	restartByName bool
	startBehavior string
	endedAlert    string // alert text when the timer expires ("" = none)
	category      string
}

type compiledTrig struct {
	trigDef
	re    *regexp.Regexp  // stdlib engine (preferred)
	re2   *regexp2.Regexp // .NET-syntax fallback
	plain string          // EnableRegex=False → lowercased substring
}

type trigAlert struct {
	text string
	at   time.Time
}

type liveTimer struct {
	id         int64
	name       string
	category   string
	startedAt  time.Time
	endsAt     time.Time
	endedAlert string
	triggerID  int
	// Set only while paused (character camped/swapped): time left and the
	// original full duration, used to re-anchor the bar on resume.
	remaining time.Duration
	total     time.Duration
	// wasLive marks a timer restored from disk that was RUNNING when the app
	// last saved — it keeps its absolute endsAt (real time kept passing in
	// game) instead of being re-anchored from `remaining`.
	wasLive bool
}

type trigActivityEntry struct {
	at        time.Time
	path      []string // Group > Sub > ... > Trigger
	triggerID int
}

const (
	trigActivityMax = 200
	trigTimersMax   = 200
	// Groups created in-app get GroupIds above this and are treated as enabled
	// for every character (they can't appear in GINA's per-character sets).
	trigLocalGroupIDBase = 1000000
)

var (
	trigStateMu    sync.Mutex
	trigActive     []*compiledTrig
	trigActiveChar string
	trigAlertCur   *trigAlert
	liveTimers     []*liveTimer
	trigActivity   []trigActivityEntry
	timerNextID    int64
	// Camp-out / logout detection: timers pause when the character leaves the
	// world and resume when their log becomes active again.
	trigLastLine time.Time                       // last log line seen (any line)
	campLastMsg  time.Time                       // last camp-countdown message seen
	pausedTimers = make(map[string][]*liveTimer) // lower(char) → preserved timers
)

// campoutRE matches EQ's camp countdown ("It will take you about 30 seconds to
// prepare your camp." then "... about N more seconds ..." every 5 seconds).
var campoutRE = regexp.MustCompile(`^It will take you about \d+ (?:more )?seconds? to prepare your camp\.$`)

// Slain messages — a dead mob's timers (debuffs on it, etc.) are meaningless,
// so any timer whose name mentions the victim is cleared.
var (
	trigSlainByRE  = regexp.MustCompile("^([\\w`' -]+) has been slain by [\\w`' -]+!$")
	trigYouSlainRE = regexp.MustCompile("^You have slain ([\\w`' -]+)!$")
)

// clearTimersMentioningLocked removes live timers whose name contains the
// slain mob's name (case-insensitive). Caller holds trigStateMu.
func clearTimersMentioningLocked(mob string) int {
	needle := strings.ToLower(strings.TrimSpace(mob))
	if needle == "" {
		return 0
	}
	keep := liveTimers[:0]
	removed := 0
	for _, lt := range liveTimers {
		if strings.Contains(strings.ToLower(lt.name), needle) {
			removed++
			continue
		}
		keep = append(keep, lt)
	}
	liveTimers = keep
	return removed
}

const (
	campIdleAfter  = 30 * time.Second // silence after a camp message → camped out
	campMsgWindow  = 60 * time.Second // how recent a camp message must be to count
	idlePauseAfter = 5 * time.Minute  // /q, /exit, crash: silence alone → pause
)

// trigPreserveCategory reports whether a category's timers survive a character
// leaving the world (paused, resumed on their next login). Buff durations and
// discipline cooldowns are frozen offline; everything else is discarded.
func trigPreserveCategory(cat string) bool {
	return strings.EqualFold(cat, "Buffs (Self)") || strings.EqualFold(cat, "Disciplines")
}

// pauseTimersLocked stops the clock for the preserved categories (stashing the
// remaining time under the character's name) and drops every other timer.
// Returns how many were preserved. Caller holds trigStateMu.
func pauseTimersLocked(charName string, now time.Time) int {
	var kept []*liveTimer
	for _, lt := range liveTimers {
		if !trigPreserveCategory(lt.category) {
			continue
		}
		rem := lt.endsAt.Sub(now)
		if rem <= 0 {
			continue
		}
		lt.total = lt.endsAt.Sub(lt.startedAt)
		lt.remaining = rem
		kept = append(kept, lt)
	}
	liveTimers = nil
	if len(kept) > 0 && charName != "" {
		key := strings.ToLower(charName)
		pausedTimers[key] = append(pausedTimers[key], kept...)
	}
	return len(kept)
}

// resumeTimersLocked restarts a character's paused timers. Camp-paused timers
// resume with their remaining time (re-anchoring startedAt so the bar fill
// stays proportional); was-live restored timers keep their absolute endsAt
// (time kept passing in game across the app restart) and are dropped if they
// expired meanwhile. Caller holds trigStateMu.
func resumeTimersLocked(charName string, now time.Time) int {
	key := strings.ToLower(charName)
	list := pausedTimers[key]
	if len(list) == 0 {
		return 0
	}
	delete(pausedTimers, key)
	n := 0
	for _, lt := range list {
		if lt.wasLive {
			lt.wasLive = false
			if !lt.endsAt.After(now) {
				continue
			}
		} else {
			lt.startedAt = now.Add(lt.remaining - lt.total)
			lt.endsAt = now.Add(lt.remaining)
			lt.remaining = 0
			lt.total = 0
		}
		if lt.id == 0 {
			timerNextID++
			lt.id = timerNextID
		}
		liveTimers = append(liveTimers, lt)
		n++
	}
	return n
}

// PauseTriggerTimers pauses the preserved categories for a character and drops
// the rest — called on a character swap (log file switch).
func PauseTriggerTimers(charName, reason string) {
	now := time.Now()
	trigStateMu.Lock()
	n := pauseTimersLocked(charName, now)
	trigStateMu.Unlock()
	if n > 0 {
		addStatus("Timers: paused %d for %s (%s)", n, charName, reason)
	}
}

// ── persistence: Buffs (Self)/Disciplines timers survive app restarts ────────
// Saved every few seconds (when changed) and on shutdown. Paused timers keep
// their frozen remaining time; running timers keep their absolute end instant
// (the game clock kept ticking while the app was down), so a quick reboot
// costs at most the save interval.

type persistedTimer struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	EndedAlert  string `json:"ended_alert,omitempty"`
	WasLive     bool   `json:"was_live,omitempty"`
	EndsAtMs    int64  `json:"ends_at_ms,omitempty"`
	StartedAtMs int64  `json:"started_at_ms,omitempty"`
	RemainingMs int64  `json:"remaining_ms,omitempty"`
	TotalMs     int64  `json:"total_ms,omitempty"`
}

type persistedTimersFile struct {
	SavedAtMs int64                       `json:"saved_at_ms"`
	Chars     map[string][]persistedTimer `json:"chars"` // lower(char) → timers
}

var (
	trigPersistMu   sync.Mutex
	trigPersistLast time.Time
	trigPersistPrev []byte
)

// snapshotTimersJSON serializes every preserved-category timer: the paused map
// plus the current character's running ones.
func snapshotTimersJSON(now time.Time) []byte {
	out := persistedTimersFile{SavedAtMs: now.UnixMilli(), Chars: map[string][]persistedTimer{}}
	trigStateMu.Lock()
	for key, list := range pausedTimers {
		for _, lt := range list {
			pt := persistedTimer{Name: lt.name, Category: lt.category, EndedAlert: lt.endedAlert}
			if lt.wasLive {
				pt.WasLive = true
				pt.EndsAtMs = lt.endsAt.UnixMilli()
				pt.StartedAtMs = lt.startedAt.UnixMilli()
			} else {
				pt.RemainingMs = int64(lt.remaining / time.Millisecond)
				pt.TotalMs = int64(lt.total / time.Millisecond)
			}
			out.Chars[key] = append(out.Chars[key], pt)
		}
	}
	if trigActiveChar != "" {
		key := strings.ToLower(trigActiveChar)
		for _, lt := range liveTimers {
			if !trigPreserveCategory(lt.category) || !lt.endsAt.After(now) {
				continue
			}
			out.Chars[key] = append(out.Chars[key], persistedTimer{
				Name: lt.name, Category: lt.category, EndedAlert: lt.endedAlert,
				WasLive: true, EndsAtMs: lt.endsAt.UnixMilli(), StartedAtMs: lt.startedAt.UnixMilli(),
			})
		}
	}
	trigStateMu.Unlock()
	data, _ := json.MarshalIndent(out, "", " ")
	return data
}

// persistTriggerTimers writes the snapshot when it changed (throttled unless
// forced). Called from the engine ticker and on shutdown.
func persistTriggerTimers(now time.Time, force bool) {
	trigPersistMu.Lock()
	defer trigPersistMu.Unlock()
	if !force && now.Sub(trigPersistLast) < 3*time.Second {
		return
	}
	trigPersistLast = now
	data := snapshotTimersJSON(now)
	if bytes.Equal(data, trigPersistPrev) {
		return
	}
	trigPersistPrev = data
	_ = os.WriteFile(trigTimersStatePath(), data, 0600)
}

// PersistTriggerTimersNow flushes the timer snapshot — called on app shutdown.
func PersistTriggerTimersNow() {
	persistTriggerTimers(time.Now(), true)
}

// loadPersistedTimers restores saved timers into the paused map at startup;
// each character's timers resume when their log becomes active.
func loadPersistedTimers() {
	data, err := os.ReadFile(trigTimersStatePath())
	if err != nil {
		return
	}
	var f persistedTimersFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	now := time.Now()
	n := 0
	trigStateMu.Lock()
	for key, list := range f.Chars {
		for _, pt := range list {
			lt := &liveTimer{name: pt.Name, category: pt.Category, endedAlert: pt.EndedAlert}
			if pt.WasLive {
				if pt.EndsAtMs <= now.UnixMilli() {
					continue // expired while the app was down
				}
				lt.wasLive = true
				lt.endsAt = time.UnixMilli(pt.EndsAtMs)
				lt.startedAt = time.UnixMilli(pt.StartedAtMs)
			} else {
				if pt.RemainingMs <= 0 {
					continue
				}
				lt.remaining = time.Duration(pt.RemainingMs) * time.Millisecond
				lt.total = time.Duration(pt.TotalMs) * time.Millisecond
				if lt.total < lt.remaining {
					lt.total = lt.remaining
				}
			}
			pausedTimers[key] = append(pausedTimers[key], lt)
			n++
		}
	}
	trigStateMu.Unlock()
	if n > 0 {
		addStatus("Timers: restored %d saved timer(s)", n)
	}
}

// startTriggerEngine loads the stored triggers and runs the expiry ticker that
// culls finished timers (firing their timer-ended alerts), pauses timers when
// the character leaves the world, and checkpoints preserved timers to disk.
func startTriggerEngine() {
	LoadTriggers()
	loadPersistedTimers()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			for _, msg := range tickTriggerTimers(now) {
				addStatus("%s", msg)
			}
			persistTriggerTimers(now, false)
		}
	}()
}

// tickTriggerTimers culls expired timers and detects "character left the
// world": a camp countdown followed by log silence, or (the /q, /exit, crash
// backup) 5 minutes with no log activity at all. Returns status messages.
func tickTriggerTimers(now time.Time) []string {
	var msgs []string
	trigStateMu.Lock()
	defer trigStateMu.Unlock()

	keep := liveTimers[:0]
	for _, lt := range liveTimers {
		if lt.endsAt.After(now) {
			keep = append(keep, lt)
			continue
		}
		if lt.endedAlert != "" {
			trigAlertCur = &trigAlert{text: lt.endedAlert, at: now}
		}
	}
	liveTimers = keep

	if trigActiveChar == "" || len(liveTimers) == 0 || trigLastLine.IsZero() {
		return msgs
	}
	idle := now.Sub(trigLastLine)
	campRecent := !campLastMsg.IsZero() && now.Sub(campLastMsg) < campMsgWindow
	if (campRecent && idle >= campIdleAfter) || idle >= idlePauseAfter {
		reason := "no log activity for 5 minutes"
		if campRecent {
			reason = "camped out"
		}
		dropped := len(liveTimers)
		kept := pauseTimersLocked(trigActiveChar, now)
		dropped -= kept
		campLastMsg = time.Time{}
		msgs = append(msgs, fmt.Sprintf(
			"Timers: %s — %s; paused %d (Buffs (Self)/Disciplines), cleared %d",
			trigActiveChar, reason, kept, dropped))
	}
	return msgs
}

// RebuildTriggerActivation recomputes and recompiles the active trigger set
// for the currently tailed character. Called at load, import, edit, and
// character swap. Compilation happens outside the locks.
func RebuildTriggerActivation() {
	charName := currentCharName

	trigStoreMu.Lock()
	var defs []trigDef
	if trigCfg != nil {
		enabled := enabledGroupsForLocked(charName)
		var walk func(g *GinaGroup, path []string)
		walk = func(g *GinaGroup, path []string) {
			p := append(append([]string{}, path...), g.Name)
			if effectiveGroupEnabledLocked(g, enabled) {
				for _, t := range g.Triggers {
					if !effectiveTriggerEnabledLocked(g, t) {
						continue
					}
					if d, ok := defFromTrigger(t, p); ok {
						defs = append(defs, d)
					}
				}
			}
			for _, c := range g.Groups {
				walk(c, p)
			}
		}
		for _, g := range trigCfg.Groups {
			walk(g, nil)
		}
	}
	trigStoreMu.Unlock()

	compiled := make([]*compiledTrig, 0, len(defs))
	for _, d := range defs {
		if c := compileTrigDef(d, charName); c != nil {
			compiled = append(compiled, c)
		}
	}

	trigStateMu.Lock()
	trigActive = compiled
	trigActiveChar = charName
	trigStateMu.Unlock()
}

// defFromTrigger snapshots the engine-relevant fields. ok=false when the
// trigger can never do anything we support (no pattern, or no action).
func defFromTrigger(t *GinaTrigger, groupPath []string) (trigDef, bool) {
	if strings.TrimSpace(t.TriggerText) == "" {
		return trigDef{}, false
	}
	durMs := t.TimerMillisecondDuration
	if durMs <= 0 {
		durMs = int64(t.TimerDuration) * 1000
	}
	isTimer := t.TimerType == "Timer" && durMs > 0
	endedAlert := ""
	if isTimer && bool(t.UseTimerEnded) && t.TimerEndedTrigger != nil &&
		bool(t.TimerEndedTrigger.UseText) && strings.TrimSpace(t.TimerEndedTrigger.DisplayText) != "" {
		endedAlert = t.TimerEndedTrigger.DisplayText
	}
	hasAlert := bool(t.UseText) && strings.TrimSpace(t.DisplayText) != ""
	if !isTimer && !hasAlert {
		return trigDef{}, false // nothing we render would happen
	}
	cat := strings.TrimSpace(t.Category)
	if cat == "" {
		cat = "Default"
	}
	return trigDef{
		id:            t.ID,
		path:          groupPath,
		name:          t.Name,
		pattern:       t.TriggerText,
		enableRegex:   bool(t.EnableRegex),
		useText:       hasAlert,
		displayText:   t.DisplayText,
		timer:         isTimer,
		timerName:     t.TimerName,
		durMs:         durMs,
		restartByName: bool(t.RestartBasedOnTimerName),
		startBehavior: t.TimerStartBehavior,
		endedAlert:    endedAlert,
		category:      cat,
	}, true
}

// compileTrigDef compiles a def against the current character ({C} tokens are
// substituted into the pattern first). Returns nil when the pattern is
// unsupported by both engines.
func compileTrigDef(d trigDef, charName string) *compiledTrig {
	pattern := substCharPattern(d.pattern, charName)
	c := &compiledTrig{trigDef: d}
	if !d.enableRegex {
		c.plain = strings.ToLower(pattern)
		return c
	}
	if re, err := regexp.Compile("(?i)" + pattern); err == nil {
		c.re = re
		return c
	}
	if re2, err := regexp2.Compile(pattern, regexp2.IgnoreCase); err == nil {
		re2.MatchTimeout = 50 * time.Millisecond
		c.re2 = re2
		return c
	}
	return nil
}

// ProcessTriggerLine matches one raw log line against the active set. Called
// from the tail loop for every line. Any line counts as "in the world", so it
// also resumes the character's paused timers and feeds camp-out detection.
func ProcessTriggerLine(line string) {
	content := logMessageContent(line)
	if content == "" {
		return
	}
	now := time.Now()

	// A slain mob's timers (debuffs on it, kill windows, etc.) are dead weight.
	mob := ""
	if m := trigSlainByRE.FindStringSubmatch(content); m != nil {
		mob = m[1]
	} else if m := trigYouSlainRE.FindStringSubmatch(content); m != nil {
		mob = m[1]
	}

	trigStateMu.Lock()
	trigLastLine = now
	if campoutRE.MatchString(content) {
		campLastMsg = now
	}
	cleared := 0
	if mob != "" {
		cleared = clearTimersMentioningLocked(mob)
	}
	resumed := 0
	if trigActiveChar != "" {
		resumed = resumeTimersLocked(trigActiveChar, now)
	}
	active := trigActive
	charName := trigActiveChar
	trigStateMu.Unlock()

	if cleared > 0 {
		addStatus("Timers: cleared %d for slain %s", cleared, mob)
	}
	if resumed > 0 {
		addStatus("Timers: resumed %d for %s", resumed, charName)
	}
	if len(active) == 0 {
		return
	}
	lower := strings.ToLower(content)
	for _, c := range active {
		caps, named, ok := c.match(content, lower)
		if ok {
			fireTrigger(c, caps, named, charName, now)
		}
	}
}

func (c *compiledTrig) match(content, lower string) (caps []string, named map[string]string, ok bool) {
	switch {
	case c.re != nil:
		m := c.re.FindStringSubmatch(content)
		if m == nil {
			return nil, nil, false
		}
		named = make(map[string]string)
		for i, n := range c.re.SubexpNames() {
			if n != "" && i < len(m) {
				named[n] = m[i]
			}
		}
		return m, named, true
	case c.re2 != nil:
		m, err := c.re2.FindStringMatch(content)
		if err != nil || m == nil {
			return nil, nil, false
		}
		named = make(map[string]string)
		for _, g := range m.Groups() {
			caps = append(caps, g.String())
			if g.Name != "" {
				named[g.Name] = g.String()
			}
		}
		return caps, named, true
	case c.plain != "":
		if strings.Contains(lower, c.plain) {
			return []string{content}, nil, true
		}
	}
	return nil, nil, false
}

// fireTrigger applies a matched trigger: activity entry, alert, and/or timer.
func fireTrigger(c *compiledTrig, caps []string, named map[string]string, charName string, now time.Time) {
	fullPath := append(append([]string{}, c.path...), c.name)

	trigStateMu.Lock()
	defer trigStateMu.Unlock()

	trigActivity = append(trigActivity, trigActivityEntry{at: now, path: fullPath, triggerID: c.id})
	if len(trigActivity) > trigActivityMax {
		trigActivity = trigActivity[len(trigActivity)-trigActivityMax:]
	}

	if c.useText {
		if txt := strings.TrimSpace(substCharText(substCaptures(c.displayText, caps, named), charName)); txt != "" {
			trigAlertCur = &trigAlert{text: txt, at: now}
		}
	}

	if !c.timer {
		return
	}
	name := strings.TrimSpace(substCharText(substCaptures(c.timerName, caps, named), charName))
	if name == "" {
		name = c.name
	}
	endedTxt := substCharText(substCaptures(c.endedAlert, caps, named), charName)
	dur := time.Duration(c.durMs) * time.Millisecond

	// Duplicate handling: RestartBasedOnTimerName resets ANY running timer with
	// the exact same (substituted) name; otherwise TimerStartBehavior applies.
	if c.restartByName {
		for _, lt := range liveTimers {
			if lt.name == name {
				lt.startedAt = now
				lt.endsAt = now.Add(dur)
				lt.endedAlert = endedTxt
				return
			}
		}
	} else {
		switch c.startBehavior {
		case "RestartTimer":
			for _, lt := range liveTimers {
				if lt.triggerID == c.id && lt.name == name {
					lt.startedAt = now
					lt.endsAt = now.Add(dur)
					lt.endedAlert = endedTxt
					return
				}
			}
		case "IgnoreIfRunning":
			for _, lt := range liveTimers {
				if lt.name == name {
					return
				}
			}
		}
	}

	timerNextID++
	liveTimers = append(liveTimers, &liveTimer{
		id: timerNextID, name: name, category: c.category,
		startedAt: now, endsAt: now.Add(dur),
		endedAlert: endedTxt, triggerID: c.id,
	})
	if len(liveTimers) > trigTimersMax {
		liveTimers = liveTimers[len(liveTimers)-trigTimersMax:]
	}
}

// ── substitution helpers ─────────────────────────────────────────────────────

var trigTokenRE = regexp.MustCompile(`\$\{([^}]*)\}`)

// substCaptures resolves GINA's ${1}/${name} tokens against a match's capture
// groups (index 0 = full match, per regex convention).
func substCaptures(tpl string, caps []string, named map[string]string) string {
	if tpl == "" || !strings.Contains(tpl, "${") {
		return tpl
	}
	return trigTokenRE.ReplaceAllStringFunc(tpl, func(m string) string {
		key := m[2 : len(m)-1]
		if n, err := strconv.Atoi(key); err == nil {
			if n >= 0 && n < len(caps) {
				return caps[n]
			}
			return ""
		}
		if v, ok := named[key]; ok {
			return v
		}
		return ""
	})
}

// substCharText replaces GINA's {C} (current character) token in display text.
func substCharText(s, charName string) string {
	if charName == "" || !strings.Contains(s, "{") {
		return s
	}
	return strings.NewReplacer("{C}", charName, "{c}", charName).Replace(s)
}

// substCharPattern replaces {C} in a regex pattern (quoted, since it lands
// inside the pattern). With no character yet, the token is left literal —
// the pattern simply won't match, same as GINA before a log is monitored.
func substCharPattern(s, charName string) string {
	if charName == "" || !strings.Contains(s, "{") {
		return s
	}
	q := regexp.QuoteMeta(charName)
	return strings.NewReplacer("{C}", q, "{c}", q).Replace(s)
}

// ── pattern-support cache (edit view "unsupported" badge) ───────────────────

var (
	trigSupportMu sync.Mutex
	trigSupport   = make(map[int]bool)
)

func patternSupported(id int, pattern string, enableRegex bool) bool {
	if !enableRegex || strings.TrimSpace(pattern) == "" {
		return true
	}
	trigSupportMu.Lock()
	if ok, seen := trigSupport[id]; seen {
		trigSupportMu.Unlock()
		return ok
	}
	trigSupportMu.Unlock()

	p := substCharPattern(pattern, "Xxxxx")
	supported := false
	if _, err := regexp.Compile("(?i)" + p); err == nil {
		supported = true
	} else if _, err := regexp2.Compile(p, regexp2.IgnoreCase); err == nil {
		supported = true
	}

	trigSupportMu.Lock()
	trigSupport[id] = supported
	trigSupportMu.Unlock()
	return supported
}

func invalidateTrigSupport(id int) {
	trigSupportMu.Lock()
	delete(trigSupport, id)
	trigSupportMu.Unlock()
}

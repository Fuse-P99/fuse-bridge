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
	visibleDurMs  int64 // GINA TimerVisibleDuration: show the bar only for the last N ms (0 = whole time)
	startBehavior string
	category      string
	enders        []enderDef // early-end conditions (empty for most triggers)
	// Text-to-speech spoken when the trigger matches (GINA UseTextToVoice).
	ttsUse       bool
	ttsInterrupt bool   // cut off any speech already playing (InterruptSpeech)
	ttsText      string // what to speak (falls back to DisplayText if unset)
	// Audio file played when the trigger matches (GINA PlayMediaFile). Bare file
	// name, resolved against the media dir at play time ("" = none).
	mediaFile string
	// Clipboard copy on match (GINA CopyToClipboard/ClipboardText).
	copyClipboard bool
	clipboardText string
	// Counter (GINA UseCounterResetTimer/CounterResetDuration): {counter} in any
	// text increments per match, resetting after counterResetMs of no match
	// (0 = never auto-reset). useCounter is set when either is in play.
	useCounter     bool
	counterResetMs int64
	// Timer end-triggers: "ending" fires endingOffset (from TimerEndingTime)
	// before expiry; "ended" fires at expiry. Each can show text, speak, and play
	// a sound. Raw templates here; substituted per match at timer creation.
	endingOffset time.Duration
	ending       endActions
	ended        endActions
}

// endActions is the text/TTS/sound a timer end-trigger performs (GINA
// TimerEndingTrigger / TimerEndedTrigger). Empty fields mean "don't".
type endActions struct {
	text         string // alert text ("" = none)
	ttsText      string // spoken text ("" = none)
	ttsInterrupt bool
	media        string // media file basename ("" = none)
}

func (e endActions) empty() bool {
	return e.text == "" && e.ttsText == "" && e.media == ""
}

// enderDef is one raw early-end condition snapshotted from the store.
type enderDef struct {
	text        string
	enableRegex bool
}

type compiledTrig struct {
	trigDef
	re     *regexp.Regexp  // stdlib engine (preferred)
	re2    *regexp2.Regexp // .NET-syntax fallback
	plain  string          // EnableRegex=False → lowercased substring
	enders []*compiledEnder
}

// compiledEnder is an early-end condition compiled the same way as a trigger
// pattern (stdlib RE2 → regexp2 fallback → plain substring).
type compiledEnder struct {
	re    *regexp.Regexp
	re2   *regexp2.Regexp
	plain string
}

// trigAlert is one fired text alert. Alerts carry their trigger's category so
// they can be colored per category and routed to a per-category overlay, the
// same way countdown bars are.
type trigAlert struct {
	id       int64
	text     string
	category string
	at       time.Time
}

type liveTimer struct {
	id        int64
	name      string
	category  string
	startedAt time.Time
	endsAt    time.Time
	triggerID int
	// visibleDurMs hides the bar until its last N ms (GINA TimerVisibleDuration);
	// 0 = visible the whole time. The timer still runs and fires end-triggers.
	visibleDurMs int64
	// Set only while paused (character camped/swapped/quit, or restored from
	// disk): time left and the original full duration, used to re-anchor the bar
	// on resume.
	remaining time.Duration
	total     time.Duration
	// End-trigger actions (pre-substituted at creation). "ending" fires once when
	// the timer has endingOffset left (endingSpoken latches it); "ended" fires at
	// expiry. endingOffset == 0 means no ending trigger.
	endingOffset time.Duration
	endingSpoken bool
	ending       endActions
	ended        endActions
}

type trigActivityEntry struct {
	at        time.Time
	path      []string // Group > Sub > ... > Trigger
	triggerID int
}

const (
	trigActivityMax = 200
	trigTimersMax   = 200
	// Alert history kept for the overlays, which stack recent alerts rather than
	// showing only the latest. The TTL is comfortably longer than the ~10s the UI
	// fades over, so a slow poll never drops an alert the user should have seen.
	trigAlertsMax = 60
	trigAlertTTL  = 30 * time.Second
	// Groups created in-app get GroupIds above this and are treated as enabled
	// for every character (they can't appear in GINA's per-character sets).
	trigLocalGroupIDBase = 1000000
)

var (
	trigStateMu    sync.Mutex
	trigActive     []*compiledTrig
	trigActiveChar string
	trigAlerts     []*trigAlert // oldest first; culled by TTL in the ticker
	alertNextID    int64
	liveTimers     []*liveTimer
	trigActivity   []trigActivityEntry
	timerNextID    int64
	// Camp-out / logout detection: timers pause when the character leaves the
	// world and resume when their log becomes active again.
	trigLastLine time.Time // last log line seen (any line)
	campLastMsg  time.Time // last camp-countdown message seen
	// trigLeftWorldAt is when a deliberate /q or /exit pause fired; it suppresses
	// the resume-on-any-line path for quitPauseGrace (see QuitTriggerTimers).
	trigLeftWorldAt time.Time
	pausedTimers    = make(map[string][]*liveTimer) // lower(char) → preserved timers
	// Per-trigger {counter} state (GINA counter). Keyed by session trigger id.
	trigCounters = make(map[int]*trigCounterState)
)

// trigCounterState tracks one trigger's {counter}: the running count and when it
// last advanced (to apply the reset window).
type trigCounterState struct {
	count  int
	lastAt time.Time
}

// advanceCounterLocked bumps a trigger's counter for a fresh match and returns
// the new value. It resets to 1 when resetMs > 0 and at least that long has
// passed since the previous match. Caller holds trigStateMu.
func advanceCounterLocked(id int, resetMs int64, now time.Time) int {
	c := trigCounters[id]
	if c == nil {
		c = &trigCounterState{}
		trigCounters[id] = c
	}
	if resetMs > 0 && !c.lastAt.IsZero() && now.Sub(c.lastAt) >= time.Duration(resetMs)*time.Millisecond {
		c.count = 0
	}
	c.count++
	c.lastAt = now
	return c.count
}

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
	campIdleAfter  = 5 * time.Second  // silence after a camp message → camped out
	campMsgWindow  = 60 * time.Second // how recent a camp message must be to count
	idlePauseAfter = 5 * time.Minute  // crash / link-dead: silence alone → pause
	// quitPauseGrace suppresses the resume-on-any-line path right after a
	// deliberate quit. The eqlog and dbg.txt are tailed independently (100ms vs
	// 1s polls), so a final eqlog line can still land after we've seen the quit
	// marker — without this, a straggler would instantly un-pause what we just
	// froze. No relogin can complete inside the window.
	quitPauseGrace = 10 * time.Second
)

// trigPreserveCategory reports whether a category's timers survive a character
// leaving the world (paused, resumed on their next login) or are discarded.
// Driven by the category's "Auto pause timers" setting — see categoryAutoPause,
// whose defaults reproduce the old hard-coded Buffs (Self)/Disciplines rule.
func trigPreserveCategory(cat string) bool {
	return categoryAutoPause(cat)
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

// dismissTimerByID removes a single live timer (the user clicked its trash bin
// on the live board). Also drops any paused copy so it doesn't resurface on a
// character swap.
func dismissTimerByID(id int64) {
	trigStateMu.Lock()
	var name string
	keep := liveTimers[:0]
	for _, lt := range liveTimers {
		if lt.id == id {
			name = lt.name
			continue
		}
		keep = append(keep, lt)
	}
	liveTimers = keep
	if name != "" {
		dropPausedNamedLocked(name)
	}
	trigStateMu.Unlock()
}

// dismissTimerCategory removes every live timer in a category (trash bin on the
// category header), plus any paused copies of those timers.
func dismissTimerCategory(cat string) {
	trigStateMu.Lock()
	var names []string
	keep := liveTimers[:0]
	for _, lt := range liveTimers {
		if lt.category == cat {
			names = append(names, lt.name)
			continue
		}
		keep = append(keep, lt)
	}
	liveTimers = keep
	for _, n := range names {
		dropPausedNamedLocked(n)
	}
	trigStateMu.Unlock()
}

// dropPausedNamedLocked removes any paused timers with the given name across all
// characters, so a dismissed buff/disc doesn't resume on the next login/swap.
// Caller holds trigStateMu.
func dropPausedNamedLocked(name string) {
	for key, list := range pausedTimers {
		kept := list[:0]
		for _, lt := range list {
			if lt.name != name {
				kept = append(kept, lt)
			}
		}
		if len(kept) == 0 {
			delete(pausedTimers, key)
		} else {
			pausedTimers[key] = kept
		}
	}
}

// liveTimerNamedLocked reports whether a live timer with the exact given name
// already exists. Used to avoid re-adding a paused timer that a fresh cast has
// already recreated. Caller holds trigStateMu.
func liveTimerNamedLocked(name string) bool {
	for _, lt := range liveTimers {
		if lt.name == name {
			return true
		}
	}
	return false
}

// resumeTimersLocked restarts a character's paused timers with the remaining
// time they were frozen at, re-anchoring startedAt so the bar fill stays
// proportional. Every paused timer is frozen — whether it was stashed by a
// camp/quit/swap or restored from disk — so there is one path here. Caller
// holds trigStateMu.
func resumeTimersLocked(charName string, now time.Time) int {
	key := strings.ToLower(charName)
	list := pausedTimers[key]
	if len(list) == 0 {
		return 0
	}
	delete(pausedTimers, key)
	n := 0
	for _, lt := range list {
		// If an equivalent timer is already live, the character recast this
		// buff/disc (a fresh, full-duration timer) before we resumed — keep that
		// one and drop the stale restored copy instead of duplicating it.
		if liveTimerNamedLocked(lt.name) {
			continue
		}
		// Auto-pause can be switched off while a timer is stashed (or restored
		// from a file written when it was still on). Honour the current setting
		// rather than resurrecting a bar the user has since said to discard —
		// without this the entry would also be re-persisted on every checkpoint
		// and outlive the setting change indefinitely.
		if !trigPreserveCategory(lt.category) {
			continue
		}
		lt.startedAt = now.Add(lt.remaining - lt.total)
		lt.endsAt = now.Add(lt.remaining)
		lt.remaining = 0
		lt.total = 0
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

// QuitTriggerTimers freezes every auto-pause category's timers on a deliberate
// /q or /exit, seen in dbg.txt (see tailDbgLog), and clears the rest. Unlike
// camping, quitting writes no countdown to the eqlog, so without this the timers
// would keep burning until the 5-minute idle fallback — which stays in place for
// the cases that produce no marker at all (crash, link-dead, alt-F4).
//
// dbg.txt is shared by every EQ instance, so any quit is acted on, including one
// from a boxed client — which also freezes the tailed character's timers. That
// self-corrects: their next log line resumes the timers once quitPauseGrace has
// passed, at the cost of a brief gap on the board and a few seconds of
// overstated duration. Freezing a box's timers early is the cheap mistake; the
// expensive one is letting a real quit burn a 36-minute buff bar to zero.
func QuitTriggerTimers() {
	now := time.Now()
	trigStateMu.Lock()
	charName := trigActiveChar
	kept, dropped := 0, 0
	if charName != "" {
		dropped = len(liveTimers)
		kept = pauseTimersLocked(charName, now)
		dropped -= kept
	}
	trigLeftWorldAt = now
	campLastMsg = time.Time{} // the camp path must not fire a second time for this exit
	trigStateMu.Unlock()

	if kept > 0 || dropped > 0 {
		addStatus("Timers: %s — quit/exit; paused %d (auto-pause categories), cleared %d",
			charName, kept, dropped)
		emitTriggersChanged()
	}
}

// ── persistence: auto-pause categories' timers survive app restarts ──────────
// Saved every few seconds and on shutdown. Everything is written FROZEN — the
// time left as of the checkpoint, never an absolute end instant: with the app
// closed we can't see the character, so we assume they're out of the world and
// stop the clock. Playing without the app running therefore leaves timers
// overstated on the next launch, which is both unavoidable and the safe
// direction to be wrong in; charging the whole app-down interval instead used
// to silently delete buffs that were actually still up.

type persistedTimer struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	// TriggerID identifies the trigger that started this timer. It must survive a
	// restart: fireTrigger's RestartTimer behaviour matches on triggerID + name,
	// so a restored timer without it can never be recognised as the same buff and
	// a recast would stack a second bar instead of refreshing the first.
	TriggerID int `json:"trigger_id,omitempty"`
	// RemainingMs/TotalMs are the frozen clock: time left, and the timer's full
	// duration (so the bar's fill stays proportional on resume).
	RemainingMs int64 `json:"remaining_ms,omitempty"`
	TotalMs     int64 `json:"total_ms,omitempty"`
	// WasLive/EndsAtMs/StartedAtMs are read-only legacy: files written before
	// app-close meant "paused" stored running timers as an absolute end. Never
	// written any more — see loadPersistedTimers for how they're converted.
	WasLive      bool  `json:"was_live,omitempty"`
	EndsAtMs     int64 `json:"ends_at_ms,omitempty"`
	StartedAtMs  int64 `json:"started_at_ms,omitempty"`
	VisibleDurMs int64 `json:"visible_dur_ms,omitempty"`
	// End-trigger actions, so a restored buff/disc still fires them.
	EndingOffsetMs int64               `json:"ending_offset_ms,omitempty"`
	EndingSpoken   bool                `json:"ending_spoken,omitempty"`
	Ending         persistedEndActions `json:"ending,omitempty"`
	Ended          persistedEndActions `json:"ended,omitempty"`
}

// persistedEndActions is the on-disk form of endActions.
type persistedEndActions struct {
	Text      string `json:"text,omitempty"`
	TtsText   string `json:"tts_text,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
	Media     string `json:"media,omitempty"`
}

func persistEnd(a endActions) persistedEndActions {
	return persistedEndActions{Text: a.text, TtsText: a.ttsText, Interrupt: a.ttsInterrupt, Media: a.media}
}

func (p persistedEndActions) toEndActions() endActions {
	return endActions{text: p.Text, ttsText: p.TtsText, ttsInterrupt: p.Interrupt, media: p.Media}
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

// fillEndPersist copies a timer's end-trigger actions + visible duration into
// its persisted form, so a restored buff/disc still fires them after a restart.
func fillEndPersist(pt *persistedTimer, lt *liveTimer) {
	pt.VisibleDurMs = lt.visibleDurMs
	pt.Ended = persistEnd(lt.ended)
	if lt.endingOffset > 0 && !lt.ending.empty() {
		pt.EndingOffsetMs = int64(lt.endingOffset / time.Millisecond)
		pt.EndingSpoken = lt.endingSpoken
		pt.Ending = persistEnd(lt.ending)
	}
}

// persistFrozen builds the on-disk form of one timer with its clock stopped at
// the given remaining/total — the only form written (see the section header).
func persistFrozen(lt *liveTimer, remaining, total time.Duration) persistedTimer {
	pt := persistedTimer{
		Name: lt.name, Category: lt.category, TriggerID: lt.triggerID,
		RemainingMs: int64(remaining / time.Millisecond),
		TotalMs:     int64(total / time.Millisecond),
	}
	fillEndPersist(&pt, lt)
	return pt
}

// snapshotTimersJSON serializes every preserved-category timer: the paused map
// (already frozen) plus the current character's running ones, frozen as of this
// checkpoint.
func snapshotTimersJSON(now time.Time) []byte {
	out := persistedTimersFile{SavedAtMs: now.UnixMilli(), Chars: map[string][]persistedTimer{}}
	trigStateMu.Lock()
	for key, list := range pausedTimers {
		for _, lt := range list {
			out.Chars[key] = append(out.Chars[key], persistFrozen(lt, lt.remaining, lt.total))
		}
	}
	if trigActiveChar != "" {
		key := strings.ToLower(trigActiveChar)
		for _, lt := range liveTimers {
			if !trigPreserveCategory(lt.category) || !lt.endsAt.After(now) {
				continue
			}
			out.Chars[key] = append(out.Chars[key],
				persistFrozen(lt, lt.endsAt.Sub(now), lt.endsAt.Sub(lt.startedAt)))
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
			lt := &liveTimer{
				name: pt.Name, category: pt.Category, triggerID: pt.TriggerID,
				visibleDurMs: pt.VisibleDurMs, ended: pt.Ended.toEndActions(),
			}
			if pt.EndingOffsetMs > 0 {
				lt.endingOffset = time.Duration(pt.EndingOffsetMs) * time.Millisecond
				lt.endingSpoken = pt.EndingSpoken
				lt.ending = pt.Ending.toEndActions()
			}
			rem := time.Duration(pt.RemainingMs) * time.Millisecond
			total := time.Duration(pt.TotalMs) * time.Millisecond
			if pt.WasLive && pt.RemainingMs == 0 {
				// Legacy entry: a running timer saved with an absolute end. Freeze
				// it at the save instant, which is what would be written today —
				// the app-down interval is not charged against it.
				at := f.SavedAtMs
				if at <= 0 {
					at = now.UnixMilli()
				}
				rem = time.Duration(pt.EndsAtMs-at) * time.Millisecond
				total = time.Duration(pt.EndsAtMs-pt.StartedAtMs) * time.Millisecond
			}
			if rem <= 0 {
				continue // already expired when it was frozen
			}
			if total < rem {
				total = rem
			}
			lt.remaining = rem
			lt.total = total
			pausedTimers[key] = append(pausedTimers[key], lt)
			n++
		}
	}
	trigStateMu.Unlock()
	if n > 0 {
		addStatus("Timers: restored %d saved timer(s)", n)
	}
}

// --- push-based UI refresh ---
//
// The Timers window and every timer overlay poll GetTriggerState once a second
// as a safety net, but CH-chain timing needs the bar on screen the moment the
// trigger fires: a cleric cues off seeing it, and a second of lag turns a
// 1-second wait into a 2-second gap that can drop the tank. emitTriggersChanged
// pushes a "triggers-changed" event so the UI refreshes immediately.
//
// Emission is coalesced: it fires straight away when idle, and during a burst of
// lines it schedules a single trailing emit instead of one per change. The
// trailing emit is guaranteed, so no state change is ever left waiting for the
// next 1s poll.
const triggerEmitMinGap = 50 * time.Millisecond

var (
	trigEmitMu      sync.Mutex
	trigEmitPending bool
	trigEmitLast    time.Time
)

func emitTriggersChanged() {
	if v3App == nil {
		return
	}
	trigEmitMu.Lock()
	defer trigEmitMu.Unlock()
	if trigEmitPending {
		return // a trailing emit is already scheduled; it covers this change too
	}
	if since := time.Since(trigEmitLast); since < triggerEmitMinGap {
		trigEmitPending = true
		go func(d time.Duration) {
			time.Sleep(d)
			trigEmitMu.Lock()
			trigEmitPending = false
			trigEmitLast = time.Now()
			trigEmitMu.Unlock()
			v3App.Event.Emit("triggers-changed")
		}(triggerEmitMinGap - since)
		return
	}
	trigEmitLast = time.Now()
	// Emitted off the tail loop so a slow dispatch can never stall log reading.
	go v3App.Event.Emit("triggers-changed")
}

// startTriggerEngine loads the stored triggers and runs the expiry ticker that
// culls finished timers (firing their timer-ended alerts), pauses timers when
// the character leaves the world, and checkpoints preserved timers to disk.
func startTriggerEngine() {
	initAudioFromSettings() // master volume/mute for TTS + media
	startTTS()              // spin up the SAPI speaker for trigger text-to-speech
	LoadTriggers()
	loadPersistedTimers()
	// Pull the guild's Fuse Triggers from the server (and seed it if we're the
	// first officer) shortly after startup, once linking/network are settled.
	go func() {
		time.Sleep(3 * time.Second)
		SyncFuseTriggers()
	}()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			msgs := tickTriggerTimers(now)
			for _, msg := range msgs {
				addStatus("%s", msg)
			}
			// Timers expired / ended-alerts fired — push instead of waiting for
			// the next poll.
			if len(msgs) > 0 {
				emitTriggersChanged()
			}
			persistTriggerTimers(now, false)
		}
	}()
}

// pushAlertLocked appends a fired alert to the history. Caller holds
// trigStateMu.
func pushAlertLocked(text, category string, now time.Time) {
	if category = strings.TrimSpace(category); category == "" {
		category = "Default"
	}
	alertNextID++
	trigAlerts = append(trigAlerts, &trigAlert{
		id: alertNextID, text: text, category: category, at: now,
	})
	if len(trigAlerts) > trigAlertsMax {
		trigAlerts = trigAlerts[len(trigAlerts)-trigAlertsMax:]
	}
}

// cullAlertsLocked drops alerts older than the TTL. Caller holds trigStateMu.
func cullAlertsLocked(now time.Time) {
	cut := 0
	for cut < len(trigAlerts) && now.Sub(trigAlerts[cut].at) > trigAlertTTL {
		cut++
	}
	if cut > 0 {
		trigAlerts = append(trigAlerts[:0], trigAlerts[cut:]...)
	}
}

// tickTriggerTimers culls expired timers and detects "character left the
// world": a camp countdown followed by log silence, or (the /q, /exit, crash
// backup) 5 minutes with no log activity at all. Returns status messages.
func tickTriggerTimers(now time.Time) []string {
	var msgs []string
	away := false
	changed := false
	var endMedia []endActions // ending/ended TTS + sound to perform after unlock

	trigStateMu.Lock()

	keep := liveTimers[:0]
	for _, lt := range liveTimers {
		if lt.endsAt.After(now) {
			// Fire the "about to end" actions once, when the timer crosses into its
			// final endingOffset window.
			if lt.endingOffset > 0 && !lt.endingSpoken && !lt.ending.empty() &&
				!now.Before(lt.endsAt.Add(-lt.endingOffset)) {
				lt.endingSpoken = true
				if lt.ending.text != "" {
					// Inherits the timer's category, so it lands in the same overlay.
					pushAlertLocked(lt.ending.text, lt.category, now)
					changed = true
				}
				endMedia = append(endMedia, lt.ending)
			}
			keep = append(keep, lt)
			continue
		}
		changed = true // a bar just left the board
		// Fire the timer-ended actions at expiry.
		if !lt.ended.empty() {
			if lt.ended.text != "" {
				pushAlertLocked(lt.ended.text, lt.category, now)
			}
			endMedia = append(endMedia, lt.ended)
		}
	}
	liveTimers = keep
	cullAlertsLocked(now)

	// "Away" (camped out or long-idle) detection is independent of whether any
	// timers are running — it also drives hiding the overlay windows.
	if trigActiveChar != "" && !trigLastLine.IsZero() {
		idle := now.Sub(trigLastLine)
		campRecent := !campLastMsg.IsZero() && now.Sub(campLastMsg) < campMsgWindow
		if (campRecent && idle >= campIdleAfter) || idle >= idlePauseAfter {
			away = true
			if len(liveTimers) > 0 {
				reason := "no log activity for 5 minutes"
				if campRecent {
					reason = "camped out"
				}
				dropped := len(liveTimers)
				kept := pauseTimersLocked(trigActiveChar, now)
				dropped -= kept
				msgs = append(msgs, fmt.Sprintf(
					"Timers: %s — %s; paused %d (auto-pause categories), cleared %d",
					trigActiveChar, reason, kept, dropped))
			}
			campLastMsg = time.Time{} // fire the pause/message only once per episode
		}
	}
	trigStateMu.Unlock()

	// Speak/play the ending & ended actions outside the lock (text alerts were
	// already pushed under it).
	for _, a := range endMedia {
		fireEndActionsMedia(a)
	}

	// An expiring bar (and any ended-alert it fired) is a visible change with no
	// log line behind it — push it instead of waiting out the 1s poll.
	if changed {
		emitTriggersChanged()
	}

	// Hide the overlays while away (latched; restored on the next log line by
	// ProcessTriggerLine). Done outside the lock — it dispatches to the UI thread.
	if away {
		setPopoutsAutoHidden(true)
	}
	return msgs
}

// triggersOfficerOnly gates the whole trigger engine to officers (and admin
// mode) while the Timers feature is in officer-only testing. The Timers tab is
// already hidden for members (App.svelte `gated` flag), but hiding the UI
// alone left the engine firing audio/TTS/alerts for everyone. Officer status
// is server-verified (isOfficerCached, refreshed on sync — a change triggers a
// rebuild) and defaults to false, so members and not-yet-checked clients fail
// closed. At release, set this to false together with removing the Timers tab
// gate in App.svelte.
const triggersOfficerOnly = true

// RebuildTriggerActivation recomputes and recompiles the active trigger set
// for the currently tailed character. Called at load, import, edit, and
// character swap. Compilation happens outside the locks.
func RebuildTriggerActivation() {
	charName := currentCharName
	if triggersOfficerOnly && !isOfficerCached() && !GetSettings().AdminMode {
		trigStateMu.Lock()
		trigActive = nil
		trigActiveChar = charName
		trigStateMu.Unlock()
		return
	}
	// Resolve the class first (drives class-specific default enablement); this
	// reads the char cache / fetches async, so do it before taking trigStoreMu.
	resolveClassFor(charName)

	trigStoreMu.Lock()
	// Enabled/disabled triggers are per character — evaluate against this toon's
	// overrides and class.
	ctx := trigCtxForLocked(charName, false)
	var defs []trigDef
	if trigCfg != nil {
		var walk func(g *GinaGroup, path []string)
		walk = func(g *GinaGroup, path []string) {
			p := append(append([]string{}, path...), g.Name)
			if effectiveGroupEnabledLocked(g, ctx) {
				for _, t := range g.Triggers {
					if !effectiveTriggerEnabledLocked(g, t, ctx) {
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
	hasAlert := bool(t.UseText) && strings.TrimSpace(t.DisplayText) != ""

	// Text-to-speech spoken on match. GINA speaks TextToVoiceText, falling back to
	// the visible alert text when that field is blank.
	ttsText := t.TextToVoiceText
	if strings.TrimSpace(ttsText) == "" {
		ttsText = t.DisplayText
	}
	hasTTS := bool(t.UseTextToVoice) && strings.TrimSpace(ttsText) != ""

	// Audio file played on match (PlayMediaFile). Stored/resolved by bare name.
	mediaFile := ""
	if bool(t.PlayMediaFile) && strings.TrimSpace(t.MediaFileName) != "" {
		mediaFile = mediaBasename(t.MediaFileName)
	}
	hasMedia := mediaFile != ""

	// End-trigger actions (only meaningful for a timer): "ending" fires before
	// expiry (gated by UseTimerEnding + TimerEndingTime), "ended" fires at expiry
	// (gated by UseTimerEnded). Each can show text, speak, and play a sound.
	var endingOffset time.Duration
	var ending, ended endActions
	if isTimer {
		if bool(t.UseTimerEnding) && t.TimerEndingTime > 0 {
			ending = endActionsFrom(t.TimerEndingTrigger)
			if !ending.empty() {
				endingOffset = time.Duration(t.TimerEndingTime) * time.Second
			}
		}
		if bool(t.UseTimerEnded) {
			ended = endActionsFrom(t.TimerEndedTrigger)
		}
	}

	// Clipboard copy on match.
	copyClipboard := bool(t.CopyToClipboard) && strings.TrimSpace(t.ClipboardText) != ""

	// Counter: active when GINA's flag is set or any text uses {counter}. The
	// reset window only applies when UseCounterResetTimer is on.
	useCounter := bool(t.UseCounterResetTimer) ||
		textHasCounter(t.DisplayText, ttsText, t.ClipboardText,
			endActionsText(t.TimerEndingTrigger), endActionsText(t.TimerEndedTrigger))
	var counterResetMs int64
	if bool(t.UseCounterResetTimer) {
		counterResetMs = int64(t.CounterResetDuration) * 1000
	}

	if !isTimer && !hasAlert && !hasTTS && !hasMedia && !copyClipboard {
		return trigDef{}, false // nothing we render, speak, play, or copy would happen
	}
	cat := strings.TrimSpace(t.Category)
	if cat == "" {
		cat = "Default"
	}
	// One trigger owns any given timer name, so GINA's separate
	// RestartBasedOnTimerName flag (restart the running same-named timer, from any
	// trigger) collapses into RestartTimer. Honor the imported flag by folding it
	// in — the app no longer exposes it as its own control.
	startBehavior := t.TimerStartBehavior
	if bool(t.RestartBasedOnTimerName) {
		startBehavior = "RestartTimer"
	}
	// Early-end conditions only matter for a timer (they end its bar). A blank
	// condition would match every line, so it's dropped here.
	var enders []enderDef
	if isTimer && t.TimerEarlyEnders != nil {
		for _, e := range t.TimerEarlyEnders.Enders {
			if strings.TrimSpace(e.EarlyEndText) == "" {
				continue
			}
			enders = append(enders, enderDef{text: e.EarlyEndText, enableRegex: bool(e.EnableRegex)})
		}
	}
	return trigDef{
		id:             t.ID,
		path:           groupPath,
		name:           t.Name,
		pattern:        t.TriggerText,
		enableRegex:    bool(t.EnableRegex),
		useText:        hasAlert,
		displayText:    t.DisplayText,
		timer:          isTimer,
		timerName:      t.TimerName,
		durMs:          durMs,
		visibleDurMs:   int64(t.TimerVisibleDuration) * 1000,
		startBehavior:  startBehavior,
		category:       cat,
		enders:         enders,
		ttsUse:         hasTTS,
		ttsInterrupt:   bool(t.InterruptSpeech),
		ttsText:        ttsText,
		mediaFile:      mediaFile,
		copyClipboard:  copyClipboard,
		clipboardText:  t.ClipboardText,
		useCounter:     useCounter,
		counterResetMs: counterResetMs,
		endingOffset:   endingOffset,
		ending:         ending,
		ended:          ended,
	}, true
}

// endActionsFrom builds the raw (unsubstituted) text/TTS/sound actions from a
// GINA end-trigger. TTS text falls back to the alert text when unset, matching
// how the on-match action resolves it.
func endActionsFrom(et *GinaEndTrigger) endActions {
	if et == nil {
		return endActions{}
	}
	var a endActions
	if bool(et.UseText) && strings.TrimSpace(et.DisplayText) != "" {
		a.text = et.DisplayText
	}
	if bool(et.UseTextToVoice) {
		txt := et.TextToVoiceText
		if strings.TrimSpace(txt) == "" {
			txt = et.DisplayText
		}
		if strings.TrimSpace(txt) != "" {
			a.ttsText = txt
			a.ttsInterrupt = bool(et.InterruptSpeech)
		}
	}
	if bool(et.PlayMediaFile) && strings.TrimSpace(et.MediaFileName) != "" {
		a.media = mediaBasename(et.MediaFileName)
	}
	return a
}

// endActionsText returns an end-trigger's texts joined, for {counter} detection.
func endActionsText(et *GinaEndTrigger) string {
	if et == nil {
		return ""
	}
	return et.DisplayText + " " + et.TextToVoiceText
}

// compileTrigDef compiles a def against the current character ({C} tokens are
// substituted into the pattern first). Returns nil when the pattern is
// unsupported by both engines.
func compileTrigDef(d trigDef, charName string) *compiledTrig {
	pattern := substCharPattern(d.pattern, charName)
	c := &compiledTrig{trigDef: d}
	// Compile the early-end conditions up front. If the main pattern turns out
	// to be unsupported this whole compiledTrig is discarded (nil return), so the
	// enders never get consulted — no need to guard on that here.
	for _, ed := range d.enders {
		if ce := compileEnder(ed, charName); ce != nil {
			c.enders = append(c.enders, ce)
		}
	}
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

// compileEnder compiles one early-end condition against the current character,
// mirroring compileTrigDef. An ender whose pattern neither engine accepts is
// skipped (returns nil) — the timer just won't end early on it.
func compileEnder(ed enderDef, charName string) *compiledEnder {
	pattern := substCharPattern(ed.text, charName)
	ce := &compiledEnder{}
	if !ed.enableRegex {
		ce.plain = strings.ToLower(pattern)
		return ce
	}
	if re, err := regexp.Compile("(?i)" + pattern); err == nil {
		ce.re = re
		return ce
	}
	if re2, err := regexp2.Compile(pattern, regexp2.IgnoreCase); err == nil {
		re2.MatchTimeout = 50 * time.Millisecond
		ce.re2 = re2
		return ce
	}
	return nil
}

// matches reports whether an early-end condition fires for this log line.
func (e *compiledEnder) matches(content, lower string) bool {
	switch {
	case e.re != nil:
		return e.re.MatchString(content)
	case e.re2 != nil:
		m, err := e.re2.FindStringMatch(content)
		return err == nil && m != nil
	case e.plain != "":
		return strings.Contains(lower, e.plain)
	}
	return false
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
	// Straggler lines written around a /q or /exit must not un-freeze what the
	// quit pause just stashed; once the grace window passes, this is a real
	// login again and the latch clears.
	if !trigLeftWorldAt.IsZero() && now.Sub(trigLeftWorldAt) >= quitPauseGrace {
		trigLeftWorldAt = time.Time{}
	}
	resumed := 0
	if trigActiveChar != "" && trigLeftWorldAt.IsZero() {
		resumed = resumeTimersLocked(trigActiveChar, now)
	}
	active := trigActive
	charName := trigActiveChar
	// Which triggers currently have a live timer — an early-end condition only
	// needs checking for those, so a trigger with enders but nothing running
	// costs just this map lookup, not a regex, per line.
	var timerTrigIDs map[int]bool
	for _, lt := range liveTimers {
		if timerTrigIDs == nil {
			timerTrigIDs = make(map[int]bool)
		}
		timerTrigIDs[lt.triggerID] = true
	}
	trigStateMu.Unlock()

	// Any log line means the player is back in the world — restore overlays that
	// were auto-hidden on camp-out/idle (idempotent).
	setPopoutsAutoHidden(false)

	if cleared > 0 {
		addStatus("Timers: cleared %d for slain %s", cleared, mob)
	}
	if resumed > 0 {
		addStatus("Timers: resumed %d for %s", resumed, charName)
	}
	changed := cleared > 0 || resumed > 0
	if len(active) > 0 {
		lower := strings.ToLower(content)
		for _, c := range active {
			caps, named, ok := c.match(content, lower)
			if ok {
				fireTrigger(c, caps, named, charName, now)
				changed = true
			}
			// End early if one of this trigger's conditions matches — but only
			// when it actually has a timer running (checked before this line, so
			// a timer just started above can't end itself on the same line).
			if len(c.enders) > 0 && timerTrigIDs[c.id] && enderMatches(c, content, lower) {
				if endTriggerTimersByID(c.id) > 0 {
					changed = true
				}
			}
		}
	}
	// Push the new state to the UI now rather than letting it wait for the next
	// 1s poll — this is the latency that matters for chain timing.
	if changed {
		emitTriggersChanged()
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

	// Advance the {counter} once per match (before any substitution uses it).
	counter := 0
	if c.useCounter {
		counter = advanceCounterLocked(c.id, c.counterResetMs, now)
	}
	sub := func(tpl string) string { return substActionText(tpl, caps, named, charName, counter) }

	if c.useText {
		if txt := strings.TrimSpace(sub(c.displayText)); txt != "" {
			pushAlertLocked(txt, c.category, now)
		}
	}

	// Speak the trigger's text-to-speech (non-blocking; safe under the lock).
	if c.ttsUse {
		if txt := strings.TrimSpace(sub(c.ttsText)); txt != "" {
			speak(txt, c.ttsInterrupt)
		}
	}

	// Play the trigger's audio file (non-blocking).
	if c.mediaFile != "" {
		playMedia(resolveMediaPath(c.mediaFile))
	}

	// Copy to the clipboard on match.
	if c.copyClipboard {
		if txt := sub(c.clipboardText); strings.TrimSpace(txt) != "" {
			copyToClipboard(txt)
		}
	}

	if !c.timer {
		return
	}
	name := strings.TrimSpace(sub(c.timerName))
	if name == "" {
		name = c.name
	}
	dur := time.Duration(c.durMs) * time.Millisecond

	// Resolve the end-trigger actions now, while the captures are in hand; they
	// fire later (ending before expiry, ended at expiry) from the ticker.
	ending := resolveEndActions(c.ending, sub)
	ended := resolveEndActions(c.ended, sub)
	endingOffset := c.endingOffset
	if ending.empty() {
		endingOffset = 0
	}

	// Duplicate handling per TimerStartBehavior. RestartTimer resets this
	// trigger's own running same-named timer (one trigger owns a name); the
	// default StartNewTimer falls through and adds another bar.
	switch c.startBehavior {
	case "RestartTimer":
		for _, lt := range liveTimers {
			// A triggerID of 0 means the timer came from a save written before
			// trigger IDs were persisted. Match those on name alone and adopt
			// them, otherwise the first recast after an upgrade stacks a second
			// bar instead of refreshing the restored one.
			if lt.name == name && (lt.triggerID == c.id || lt.triggerID == 0) {
				lt.triggerID = c.id
				lt.startedAt = now
				lt.endsAt = now.Add(dur)
				lt.visibleDurMs = c.visibleDurMs
				lt.ended = ended
				// Re-arm the ending trigger for the fresh duration.
				lt.endingOffset = endingOffset
				lt.ending = ending
				lt.endingSpoken = false
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

	timerNextID++
	liveTimers = append(liveTimers, &liveTimer{
		id: timerNextID, name: name, category: c.category,
		startedAt: now, endsAt: now.Add(dur), triggerID: c.id,
		visibleDurMs: c.visibleDurMs,
		endingOffset: endingOffset, ending: ending, ended: ended,
	})
	if len(liveTimers) > trigTimersMax {
		liveTimers = liveTimers[len(liveTimers)-trigTimersMax:]
	}
}

// resolveEndActions substitutes an end-trigger's templates for a specific match.
func resolveEndActions(a endActions, sub func(string) string) endActions {
	return endActions{
		text:         strings.TrimSpace(sub(a.text)),
		ttsText:      strings.TrimSpace(sub(a.ttsText)),
		ttsInterrupt: a.ttsInterrupt,
		media:        a.media,
	}
}

// fireEndActions performs an end-trigger's resolved actions (called outside the
// state lock so the alert push is done by the caller; here we only speak/play).
func fireEndActionsMedia(a endActions) {
	if a.ttsText != "" {
		speak(a.ttsText, a.ttsInterrupt)
	}
	if a.media != "" {
		playMedia(resolveMediaPath(a.media))
	}
}

// copyToClipboard puts text on the system clipboard (best effort; no-op before
// the Wails app is running).
func copyToClipboard(text string) {
	if v3App == nil {
		return
	}
	v3App.Clipboard.SetText(text)
}

// enderMatches reports whether any of a trigger's early-end conditions fires for
// this log line.
func enderMatches(c *compiledTrig, content, lower string) bool {
	for _, e := range c.enders {
		if e.matches(content, lower) {
			return true
		}
	}
	return false
}

// endTriggerTimersByID removes every live timer that trigger id started, as an
// early-end condition just matched. Unlike natural expiry (tickTriggerTimers)
// this fires NO timer-ended alert — the effect was cancelled, not completed.
// Paused copies are dropped too, so an early-ended buff/disc doesn't resume on
// the next login. Returns how many were removed.
func endTriggerTimersByID(id int) int {
	trigStateMu.Lock()
	defer trigStateMu.Unlock()
	keep := liveTimers[:0]
	var names []string
	removed := 0
	for _, lt := range liveTimers {
		if lt.triggerID == id {
			removed++
			names = append(names, lt.name)
			continue
		}
		keep = append(keep, lt)
	}
	liveTimers = keep
	for _, n := range names {
		dropPausedNamedLocked(n)
	}
	return removed
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

// textHasCounter reports whether any text uses the {counter} token.
func textHasCounter(ss ...string) bool {
	for _, s := range ss {
		if strings.Contains(s, "{counter}") {
			return true
		}
	}
	return false
}

// substCounter replaces {counter} with the current count.
func substCounter(s string, counter int) string {
	if !strings.Contains(s, "{counter}") {
		return s
	}
	return strings.ReplaceAll(s, "{counter}", strconv.Itoa(counter))
}

// substActionText applies every substitution — regex captures (${n}/${name}),
// the character token ({C}), and the counter ({counter}) — to one action string.
func substActionText(tpl string, caps []string, named map[string]string, charName string, counter int) string {
	return substCounter(substCharText(substCaptures(tpl, caps, named), charName), counter)
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

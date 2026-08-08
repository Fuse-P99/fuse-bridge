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
	id int
	// muteKey is the trigger's stable identity (trigToggleKey: "GroupID/Name"),
	// carried onto every timer it starts so end-trigger audio can look the mute
	// up by something that survives a re-index. See liveTimer.trigKey.
	muteKey       string
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
	// muted silences this trigger's AUDIO only (match sound, TTS, timer end
	// sounds) — alerts, timer bars, and clipboard still happen. Resolved at
	// activation build from the trigger's own mute or any ancestor group's
	// (trigger_mutes.go).
	muted bool
	// clipBlocked stops this trigger taking the system clipboard, leaving every
	// other action untouched. Resolved the same way as muted, from the trigger's
	// own block or any ancestor group's (trigger_clipboard.go).
	clipBlocked bool
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
	// gina holds the GINA shorthand tokens ({S}/{N}/{TS}) expanded into the
	// compiled pattern — see expandGinaTokens. Empty for token-free patterns.
	gina []ginaToken
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
	// trigKey is the owning trigger's STABLE identity ("GroupID/Name", the same
	// key trigger_mutes.go stores under). The end-trigger audio gate looks the
	// mute up by this rather than by triggerID: triggerID is a session number
	// handed out in tree-walk order and reassigned on every re-index, so a Fuse
	// republish or a restart after a package update silently repoints it at a
	// different trigger — and the timer's ending/ended sound escaped the mute.
	trigKey string
	// path is the owning trigger's ancestor group chain ("Fuse Triggers" >
	// ... > folder). Lets the UI select timers by shared-package folder (the
	// raid card's Other Timers section: Ring War waves, future mob AE timers)
	// without a per-poll tree walk.
	path []string
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
	// trigMutedIDs: session trigger ids whose AUDIO is muted, rebuilt with
	// trigActive. The ticker consults it for running timers' end sounds.
	trigMutedIDs map[int]bool
	// trigMutedKeys: the same set keyed by the STABLE trigger key, which is what
	// the running-timer gate actually consults. Session ids shift on re-index;
	// these don't.
	trigMutedKeys map[string]bool
	trigAlerts    []*trigAlert // oldest first; culled by TTL in the ticker
	alertNextID   int64
	liveTimers    []*liveTimer
	trigActivity  []trigActivityEntry
	timerNextID   int64
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
	// campIdleAfter is the log silence that infers a completed camp. This is now
	// the FALLBACK: dbg.txt states the camp outright the moment it happens
	// (dbgCampMarker) and normally fires first, several seconds sooner. This path
	// only matters where dbg.txt can't be read.
	//
	// It has to outlast the gap between the final countdown line and the
	// character actually dropping, because unrelated periodic messages ("You are
	// low on food." on its own ~46s cycle) keep landing in that gap:
	//
	//	21:47:11  It will take about 5 more seconds to prepare your camp.
	//	21:47:15  You are low on food.        ← 4s later, still in world
	//
	// At 5s a straggler like that could arrive just AFTER the pause fired, and
	// since any log line resumes a camped character's timers, it would un-pause
	// everything — while the pause had already cleared campLastMsg, so camp
	// detection could not fire again for that episode. 8s clears the ~5s window
	// between the last countdown and the drop; nothing is written to the log once
	// the character is out.
	campIdleAfter  = 8 * time.Second
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

// ── zone-load freeze ────────────────────────────────────────────────────────
//
// The gap between "LOADING, PLEASE WAIT..." and "You have entered <zone>." is
// substantial — seconds to tens of seconds — and during it the character isn't
// being ticked by any zone, so buff durations don't advance. A bar that kept
// counting through the load lands short by the whole load time, every zone,
// cumulatively across a night of travel.
//
// So while a load is in progress the character-state timers are slid forward in
// step with real time, which holds their remaining time constant. Sliding
// rather than flagging means they also can't EXPIRE mid-load: a buff with three
// seconds left when you zone still has three seconds when you arrive, instead
// of firing its faded alert against a loading screen.
//
// World timers — spawn windows, raid calls — are deliberately not frozen. Those
// keep running whether or not you're in the world.
//
// Which categories count as character state is the existing "Auto pause timers"
// setting, whose defaults are exactly Buffs (Self) and Disciplines. Reusing it
// means no new switch to explain, and a category the user has already told us
// tracks their character follows the same rule here.

// maxZoneLoadFreeze bounds a freeze. Loads are seconds; this only limits the
// damage when the "You have entered" line never arrives at all — a crash on the
// load screen, a log rotation mid-zone, or a zone name we fail to parse. Better
// to lose a few seconds of accuracy than to hold buff bars still indefinitely.
const maxZoneLoadFreeze = 3 * time.Minute

var (
	// zoneLoadSince is when the load began (zero when not zoning), and
	// zoneLoadCredited is the last instant the frozen timers were slid forward.
	// Both guarded by trigStateMu.
	zoneLoadSince    time.Time
	zoneLoadCredited time.Time
)

// creditZoneLoadLocked slides the frozen categories' timers forward to `now`,
// so their remaining time is unchanged by however long the load has taken.
// Returns how many timers moved. No-op when not zoning. Caller holds
// trigStateMu.
func creditZoneLoadLocked(now time.Time) int {
	if zoneLoadSince.IsZero() {
		return 0
	}
	if now.Sub(zoneLoadSince) > maxZoneLoadFreeze {
		// We never saw the zone-in. Let the clocks run again rather than hold
		// them forever on a load that evidently isn't finishing.
		zoneLoadSince, zoneLoadCredited = time.Time{}, time.Time{}
		return 0
	}
	d := now.Sub(zoneLoadCredited)
	if d <= 0 {
		return 0
	}
	zoneLoadCredited = now
	n := 0
	for _, lt := range liveTimers {
		if !trigPreserveCategory(lt.category) {
			continue
		}
		// Both ends move by the same amount: shifting only endsAt would stretch
		// the bar's total and make it render as though the buff got longer.
		lt.startedAt = lt.startedAt.Add(d)
		lt.endsAt = lt.endsAt.Add(d)
		n++
	}
	return n
}

// NoteZoneLoading starts a zone-load freeze. Repeat LOADING lines within one
// load keep the original start, so the cap measures the whole load.
func NoteZoneLoading(at time.Time) {
	trigStateMu.Lock()
	if zoneLoadSince.IsZero() {
		zoneLoadSince, zoneLoadCredited = at, at
	}
	trigStateMu.Unlock()
}

// NoteZoneEntered ends the freeze, applying the last slice of held time.
// Returns how long the load took and how many timers were held, both zero when
// no freeze was running (or it had already blown the cap).
func NoteZoneEntered(at time.Time) (time.Duration, int) {
	trigStateMu.Lock()
	defer trigStateMu.Unlock()
	if zoneLoadSince.IsZero() {
		return 0, 0
	}
	held := at.Sub(zoneLoadSince)
	overCap := held < 0 || held > maxZoneLoadFreeze
	creditZoneLoadLocked(at)
	zoneLoadSince, zoneLoadCredited = time.Time{}, time.Time{}
	if overCap {
		return 0, 0
	}
	// Count the bars the freeze covered, rather than the ones the FINAL credit
	// happened to move. A tick landing on the same instant as the zone-in leaves
	// nothing left to credit, which says nothing about whether the load was
	// held — deriving the count from it reported "0 bars" on a working freeze.
	n := 0
	for _, lt := range liveTimers {
		if trigPreserveCategory(lt.category) {
			n++
		}
	}
	return held, n
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

// LeftWorldTriggerTimers freezes every auto-pause category's timers and clears
// the rest, on a definitive "character has left the world" marker from dbg.txt
// (see tailDbgLog). reason names the exit for the status line.
//
// dbg.txt states the exit outright, at the instant it happens, which is why both
// callers prefer it to anything inferred from the eqlog. The eqlog-silence paths
// remain as fallbacks for exits that write no marker at all (crash, link-dead,
// alt-F4) and for installs where dbg.txt can't be read.
//
// dbg.txt is shared by every EQ instance, so any exit is acted on, including one
// from a boxed client — which also freezes the tailed character's timers. That
// self-corrects: their next log line resumes the timers once quitPauseGrace has
// passed, at the cost of a brief gap on the board and a few seconds of
// overstated duration. Freezing a box's timers early is the cheap mistake; the
// expensive one is letting a real exit burn a 36-minute buff bar to zero.
func LeftWorldTriggerTimers(reason string) {
	now := time.Now()
	trigStateMu.Lock()
	charName := trigActiveChar
	kept, dropped := 0, 0
	if charName != "" {
		dropped = len(liveTimers)
		kept = pauseTimersLocked(charName, now)
		dropped -= kept
	}
	// Suppresses the resume-on-any-line path for quitPauseGrace, so a straggler
	// line written around the exit can't instantly un-freeze what we just
	// stashed.
	trigLeftWorldAt = now
	// Disarm the eqlog camp-silence fallback: it must not fire a second time for
	// an exit dbg.txt has already reported.
	campLastMsg = time.Time{}
	trigStateMu.Unlock()

	if kept > 0 || dropped > 0 {
		addStatus("Timers: %s — %s; paused %d (auto-pause categories), cleared %d",
			charName, reason, kept, dropped)
		emitTriggersChanged()
	}
}

// QuitTriggerTimers handles a deliberate /q or /exit.
func QuitTriggerTimers() { LeftWorldTriggerTimers("quit/exit") }

// CampTriggerTimers handles a completed camp-out. EQ writes its marker the
// moment the character actually drops, so this replaces guessing from a
// countdown in the eqlog followed by a silence window.
func CampTriggerTimers() { LeftWorldTriggerTimers("camped out") }

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
	// TrigKey is the stable "GroupID/Name". Unlike TriggerID — a session number
	// reassigned every time the tree is indexed — this still names the same
	// trigger after the shared package changes shape, which is exactly what the
	// end-audio mute gate needs across a restart.
	TrigKey string `json:"trig_key,omitempty"`
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
		Name: lt.name, Category: lt.category, TriggerID: lt.triggerID, TrigKey: lt.trigKey,
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
				name: pt.Name, category: pt.Category, triggerID: pt.TriggerID, trigKey: pt.TrigKey,
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

// worldAlarmCategory is the alert category server-timer alarms land in, so they
// can be styled and placed like any other — and so an alarm leaves something to
// look at when the sound was missed or muted.
const worldAlarmCategory = "Server Timers"

// PushWorldAlarmAlert surfaces a fired server-timer alarm in the alerts overlay.
func PushWorldAlarmAlert(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	trigStateMu.Lock()
	pushAlertLocked(text, worldAlarmCategory, time.Now())
	trigStateMu.Unlock()
	emitTriggersChanged()
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

	// Hold the character-state timers still for the duration of a zone load.
	// Done before the expiry pass so a bar that would have run out mid-load is
	// carried across instead of firing at a loading screen.
	creditZoneLoadLocked(now)

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
				// Muted triggers keep the alert text but lose the sound/TTS —
				// checked live so a mid-timer mute silences the tail too.
				if !timerAudioMutedLocked(lt) {
					endMedia = append(endMedia, lt.ending)
				}
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
			if !timerAudioMutedLocked(lt) {
				endMedia = append(endMedia, lt.ended)
			}
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
			if campRecent {
				// Camping counts as leaving the zone for quest waypoints.
				go questMarkersCamp()
			}
			// Take the character off the shared map. This is the eqlog-only
			// fallback for installs where dbg.txt can't be read (which normally
			// reports the exit first); on the plain 5-minute idle path the
			// position TTL has already expired, so it costs one dropped request.
			SendMapLocClear(trigActiveChar, "left the world")
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

// triggersOfficerOnly gated the trigger engine to officers during the Timers
// testing period. RELEASED: false since the general rollout (the Timers tab
// gate in App.svelte was dropped at the same time). Everyone gets the engine —
// linked members run the shared Fuse set + Personal; unlinked users run
// Personal only (assembleLocked never includes fuseRoot unlinked). Kept as a
// switch for an emergency re-gate; if re-enabled, note the check below reads
// GetSettings().AdminMode RAW so a View-as preview (viewas.go) keeps the
// engine running for the previewing admin.
const triggersOfficerOnly = false

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
		var walk func(g *GinaGroup, path []string, mutedInherit, clipInherit bool)
		walk = func(g *GinaGroup, path []string, mutedInherit, clipInherit bool) {
			p := append(append([]string{}, path...), g.Name)
			// Audio mutes and clipboard blocks both nest: setting either on a
			// group applies it to everything beneath.
			gMuted := mutedInherit || trigMuteGroups[g.GroupID]
			gClip := clipInherit || trigClipGroups[g.GroupID]
			// No group-level gate here: effectiveTriggerEnabledLocked already
			// folds the group chain in, AND lets an explicit trigger-level
			// override beat it — a timer switched on by hand fires inside a
			// disabled group. The old outer gate silently killed exactly that.
			for _, t := range g.Triggers {
				if !effectiveTriggerEnabledLocked(g, t, ctx) {
					continue
				}
				if d, ok := defFromTrigger(t, p); ok {
					key := trigToggleKey(g, t)
					d.muteKey = key
					d.muted = gMuted || trigMuteTriggers[key]
					d.clipBlocked = gClip || trigClipTriggers[key]
					defs = append(defs, d)
				}
			}
			for _, c := range g.Groups {
				walk(c, p, gMuted, gClip)
			}
		}
		for _, g := range trigCfg.Groups {
			walk(g, nil, false, false)
		}
	}
	trigStoreMu.Unlock()

	compiled := make([]*compiledTrig, 0, len(defs))
	for _, d := range defs {
		if c := compileTrigDef(d, charName); c != nil {
			compiled = append(compiled, c)
		}
	}

	// Mute index for RUNNING timers: end-trigger sounds fire from the ticker
	// long after the match, so they look mutes up live — muting mid-timer
	// silences that timer's ending/ended audio too.
	mutedIDs := map[int]bool{}
	mutedKeys := map[string]bool{}
	for _, c := range compiled {
		if c.muted {
			mutedIDs[c.id] = true
			if c.muteKey != "" {
				mutedKeys[c.muteKey] = true
			}
		}
	}

	trigStateMu.Lock()
	trigActive = compiled
	trigActiveChar = charName
	trigMutedIDs = mutedIDs
	trigMutedKeys = mutedKeys
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
	// GINA shorthand ({S}/{N}/{TS}) expands to capture groups before compiling;
	// the named-group spelling differs per engine, so expand per attempt.
	goPat, goToks := expandGinaTokens(pattern, false)
	if re, err := regexp.Compile("(?i)" + goPat); err == nil {
		c.re, c.gina = re, goToks
		return c
	}
	netPat, netToks := expandGinaTokens(pattern, true)
	if re2, err := regexp2.Compile(netPat, regexp2.IgnoreCase); err == nil {
		re2.MatchTimeout = 50 * time.Millisecond
		c.re2, c.gina = re2, netToks
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
	// Enders get token EXPANSION only (so a {S}-bearing ender still matches);
	// the guard/duration semantics belong to the main pattern.
	goPat, _ := expandGinaTokens(pattern, false)
	if re, err := regexp.Compile("(?i)" + goPat); err == nil {
		ce.re = re
		return ce
	}
	netPat, _ := expandGinaTokens(pattern, true)
	if re2, err := regexp2.Compile(netPat, regexp2.IgnoreCase); err == nil {
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
			if n == "" || i >= len(m) {
				continue
			}
			// GINA (i.e. .NET) lets the SAME group name appear in more than one
			// alternation branch — the Old Sebilis timers use
			// "(?:myconid (?<myconid>…) has been slain by …|You have slain
			// myconid (?<myconid>…))". Go permits the duplicate too, and exposes
			// each occurrence as its own numbered group. Only the branch that
			// actually matched has a value; the others are empty. So take the
			// first NON-EMPTY one and never let a later empty branch clobber it.
			// Assigning unconditionally made "${myconid}" render blank whenever
			// the winning branch wasn't the last one — which is why these timers
			// came out as "Static ()" when someone else got the killshot but read
			// correctly when you did.
			if v, seen := named[n]; seen && v != "" {
				continue
			}
			named[n] = m[i]
		}
		// GINA numeric guards ({N>469}): a match that fails one is no match.
		if !c.ginaGuardsPass(named) {
			return nil, nil, false
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
			if g.Name == "" {
				continue
			}
			// Same first-non-empty rule as the Go path above, for the same
			// duplicate-name reason. regexp2 usually merges same-named groups
			// the way .NET does, but this costs nothing and keeps the two
			// engines behaving identically.
			if v, seen := named[g.Name]; seen && v != "" {
				continue
			}
			named[g.Name] = g.String()
		}
		if !c.ginaGuardsPass(named) {
			return nil, nil, false
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
	if len(c.gina) > 0 {
		// GINA tokens substitute into action text too: "{S}" in a timer name
		// becomes the captured value. Both the exact spelling from the pattern
		// ("{N>469}") and the bare form users write in text ("{N}") resolve.
		base := sub
		sub = func(tpl string) string {
			s := base(tpl)
			for _, tk := range c.gina {
				v, ok := named[tk.group]
				if !ok {
					continue
				}
				short := "{" + tk.kind + tk.idx + "}"
				for _, lbl := range []string{tk.label, strings.ToLower(tk.label), short, strings.ToLower(short)} {
					s = strings.ReplaceAll(s, lbl, v)
				}
			}
			return s
		}
	}

	if c.useText {
		if txt := strings.TrimSpace(sub(c.displayText)); txt != "" {
			pushAlertLocked(txt, c.category, now)
		}
	}

	// Speak the trigger's text-to-speech (non-blocking; safe under the lock).
	// Muted triggers skip audio only — everything else above/below still runs.
	if c.ttsUse && !c.muted {
		if txt := strings.TrimSpace(sub(c.ttsText)); txt != "" {
			speak(txt, c.ttsInterrupt)
		}
	}

	// Play the trigger's audio file (non-blocking).
	if c.mediaFile != "" && !c.muted {
		playMedia(resolveMediaPath(c.mediaFile))
	}

	// Copy to the clipboard on match, unless the user blocked it here or on an
	// ancestor group. Everything else the trigger does still happens.
	if c.copyClipboard && !c.clipBlocked {
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
	// GINA {TS}: the captured timespan IS the duration — the "/t vopuk_5:00"
	// custom-timer idiom. Overrides whatever fixed duration the trigger holds.
	for _, tk := range c.gina {
		if tk.kind == "TS" {
			if ms := parseGinaTimespan(named[tk.group]); ms > 0 {
				dur = time.Duration(ms) * time.Millisecond
			}
		}
	}

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
			if lt.name != name || !sameTriggerLocked(lt, c) {
				continue
			}
			lt.triggerID = c.id
			lt.trigKey = c.muteKey
			lt.path = c.path
			// The restarting trigger owns the bar now, so it owns where the bar
			// lives too. Without this a personal trigger that restarted a bar
			// started by a same-named Fuse trigger left it sitting in the FUSE
			// trigger's category — the bar moved to the wrong overlay and no
			// amount of editing the personal trigger's category fixed it.
			lt.category = c.category
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
		startedAt: now, endsAt: now.Add(dur), triggerID: c.id, trigKey: c.muteKey,
		path:         c.path,
		visibleDurMs: c.visibleDurMs,
		endingOffset: endingOffset, ending: ending, ended: ended,
	})
	if len(liveTimers) > trigTimersMax {
		liveTimers = liveTimers[len(liveTimers)-trigTimersMax:]
	}
}

// sameTriggerLocked reports whether a running bar was started by trigger c —
// i.e. whether c's RestartTimer should refresh it rather than add a second bar.
// Caller holds trigStateMu.
//
// Matched on the stable trigger key when both sides have one. The session id is
// reassigned on every re-index, so after a Fuse republish (or a restart with a
// changed package) a personal trigger can inherit the id a Fuse trigger held
// when the bar was created — and then "restart" a bar that was never its own.
// Ids remain the fallback for bars restored from saves written before trigKey
// existed, and a zero id still matches on name alone so an upgraded client
// refreshes those instead of stacking a duplicate.
func sameTriggerLocked(lt *liveTimer, c *compiledTrig) bool {
	if lt.trigKey != "" && c.muteKey != "" {
		return lt.trigKey == c.muteKey
	}
	return lt.triggerID == c.id || lt.triggerID == 0
}

// timerAudioMutedLocked reports whether a running timer's end-trigger audio
// (ending/ended sound and TTS) is muted. Caller holds trigStateMu.
//
// Keyed on the timer's STABLE trigger key, not its session id. The id is handed
// out in tree-walk order and reassigned on every re-index, so a Fuse republish —
// or a restart after the package changed shape — repoints it at some other
// trigger, and the mute silently stopped applying. The match-time audio never
// had this problem because it reads c.muted off the freshly compiled trigger,
// which is why muting appeared to work everywhere except the timer's tail.
//
// The id is still consulted as a fallback so timers restored from a save
// written before TrigKey existed keep whatever protection they had.
func timerAudioMutedLocked(lt *liveTimer) bool {
	if lt.trigKey != "" && trigMutedKeys[lt.trigKey] {
		return true
	}
	return trigMutedIDs[lt.triggerID]
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

// ── GINA shorthand tokens ───────────────────────────────────────────────────
//
// GINA patterns aren't pure regex: {S}/{S1}…, {N}/{N1}…, and {TS} are GINA's
// own shorthand, preprocessed into capture groups before compilation. Beyond
// matching, two carry extra semantics GINA implements outside the regex:
//
//   {N>469}   a numeric comparison — the line only counts as a match when the
//             captured number passes it (>, <, >=, <=, =).
//   {TS}      a timespan ("5:00", "70:10", "1:10:00", bare seconds) whose
//             captured value BECOMES the timer's duration. This is the
//             custom-timer idiom: "/t vopuk_5:00" produces the log line
//             "vopuk_5:00 is not online at this time." and the package's
//             "^{S}_{TS} is not online…" trigger starts a 5:00 timer named
//             by {S}.
//
// Tokens also substitute into timer names / display text / TTS ({S}, {N1},
// {TS} in any action text become the captured values). {C} is handled
// separately above (it's a literal substitution, not a capture).
//
// Caveat, matching GINA's own behavior: each token consumes a capture-group
// slot, so a pattern mixing tokens with plain (…) groups shifts the ${n}
// numbering of groups that follow a token — reference tokens by token and
// groups by name/number before any token, and everything lines up.

type ginaToken struct {
	group string // generated capture-group name
	kind  string // "S" | "N" | "TS"
	idx   string // the token's index digits ("" for {S}, "1" for {S1})
	label string // the token exactly as written, for action-text substitution
	op    string // numeric comparison operator ("" = none)
	val   int
}

var ginaTokenRE = regexp.MustCompile(`\{(?i:(TS|S|N))([0-9]*)(?:\s*(>=|<=|=|>|<)\s*([0-9]+))?\}`)

// expandGinaTokens rewrites GINA tokens in a pattern into capture groups.
// netSyntax selects the named-group spelling: Go's (?P<name>…) or .NET's
// (?<name>…) for the regexp2 fallback engine.
func expandGinaTokens(pattern string, netSyntax bool) (string, []ginaToken) {
	if !strings.Contains(pattern, "{") {
		return pattern, nil
	}
	var toks []ginaToken
	out := ginaTokenRE.ReplaceAllStringFunc(pattern, func(m string) string {
		sm := ginaTokenRE.FindStringSubmatch(m)
		tk := ginaToken{
			group: fmt.Sprintf("gts%d", len(toks)),
			kind:  strings.ToUpper(sm[1]),
			idx:   sm[2],
			label: m,
		}
		var body string
		switch tk.kind {
		case "TS":
			body = `\d+(?::\d{1,2}){0,2}`
		case "N":
			body = `\d+`
			if sm[3] != "" {
				tk.op = sm[3]
				tk.val, _ = strconv.Atoi(sm[4])
			}
		default: // S — any text, lazily, so surrounding literals still anchor
			body = `.+?`
		}
		toks = append(toks, tk)
		if netSyntax {
			return "(?<" + tk.group + ">" + body + ")"
		}
		return "(?P<" + tk.group + ">" + body + ")"
	})
	return out, toks
}

// ginaGuardsPass enforces the numeric comparisons ({N>469}) after a regex
// match — regex alone can't compare numbers, so a match that fails a guard is
// treated as no match at all, exactly as GINA does.
func (c *compiledTrig) ginaGuardsPass(named map[string]string) bool {
	for _, tk := range c.gina {
		if tk.op == "" {
			continue
		}
		n, err := strconv.Atoi(named[tk.group])
		if err != nil {
			return false
		}
		ok := false
		switch tk.op {
		case ">":
			ok = n > tk.val
		case "<":
			ok = n < tk.val
		case ">=":
			ok = n >= tk.val
		case "<=":
			ok = n <= tk.val
		case "=":
			ok = n == tk.val
		}
		if !ok {
			return false
		}
	}
	return true
}

// parseGinaTimespan converts a {TS} capture to milliseconds: bare seconds,
// mm:ss (minutes may exceed 59 — "70:10" is 70 minutes), or h:mm:ss.
// Returns 0 for anything unparsable.
func parseGinaTimespan(s string) int64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return 0
	}
	total := int64(0)
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + int64(n)
	}
	return total * 1000
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

	// Expand GINA shorthand the same way compilation does, so a {S}/{TS}
	// trigger isn't flagged "unsupported" in the edit view.
	p := substCharPattern(pattern, "Xxxxx")
	goPat, _ := expandGinaTokens(p, false)
	netPat, _ := expandGinaTokens(p, true)
	supported := false
	if _, err := regexp.Compile("(?i)" + goPat); err == nil {
		supported = true
	} else if _, err := regexp2.Compile(netPat, regexp2.IgnoreCase); err == nil {
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

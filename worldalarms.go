package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reminders on the server-wide timers board (Scout Charisa, Ring 8, the Ring
// War, the boats, and earthquakes).
//
// Purely local: alarms are this player's, stored beside settings.json, and
// nothing about them reaches the server. The board itself is shared; who wants
// waking up for what is not.
//
// Two shapes of alarm fall out of one rule. Most entries have a PREDICTED
// instant, and the alarm fires LeadMs before it. An earthquake has no predicted
// instant — it's the one thing on the board nobody can schedule — so its target
// is when it happened and the lead is meaningless; the same "fire once per
// distinct target" logic then means "tell me when a new quake lands".

// worldAlarmKind distinguishes the two so the UI can hide the lead-time control
// where it would be nonsense.
const (
	alarmKindLead       = "lead"       // fires LeadMs before a predicted instant
	alarmKindOccurrence = "occurrence" // fires when the thing has happened
)

// alarmQuakeKey is the fixed key for the earthquake alarm.
const alarmQuakeKey = "quake"

// WorldAlarm is one reminder.
type WorldAlarm struct {
	// Key identifies what is being watched: "event:<key>", "boat:<key>:a",
	// "boat:<key>:b", or "quake".
	Key   string `json:"key"`
	Label string `json:"label"` // what to call it when speaking/alerting
	// LeadMs is how far ahead of the event to fire. Ignored for occurrence
	// alarms.
	LeadMs int64  `json:"lead_ms"`
	Sound  string `json:"sound"` // media file name; "" = silent
	Speak  bool   `json:"speak"`
	// SpeakText overrides the generated phrase. Empty = generated.
	SpeakText string `json:"speak_text"`
	// Repeat keeps the alarm after it fires; one-shot alarms delete themselves.
	Repeat bool `json:"repeat"`
	// FiredFor is the target instant this alarm last fired for, so one target
	// fires once however often the board is polled. Not user-facing.
	FiredFor int64 `json:"fired_for"`
}

// alarmTarget is one alarmable instant resolved from the board.
type alarmTarget struct {
	atMs  int64
	label string
	kind  string
}

var (
	waMu     sync.Mutex
	waAlarms = map[string]*WorldAlarm{}
	waLoaded bool
	// waArmed is false until the first poll has run. That poll records what is
	// already past instead of firing it — otherwise launching the app would set
	// off every alarm whose moment slipped by while it was closed, and a quake
	// from last night would announce itself as news.
	waArmed bool
)

func worldAlarmsPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "worldalarms.json")
}

func loadWorldAlarmsLocked() {
	if waLoaded {
		return
	}
	waLoaded = true
	b, err := os.ReadFile(worldAlarmsPath())
	if err != nil {
		return
	}
	var list []WorldAlarm
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for i := range list {
		a := list[i]
		if a.Key == "" {
			continue
		}
		waAlarms[a.Key] = &a
	}
}

func saveWorldAlarmsLocked() {
	list := make([]WorldAlarm, 0, len(waAlarms))
	for _, a := range waAlarms {
		list = append(list, *a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(worldAlarmsPath()), 0700)
	_ = os.WriteFile(worldAlarmsPath(), b, 0600)
}

// GetWorldAlarms returns every configured alarm, sorted by key.
func GetWorldAlarms() []WorldAlarm {
	waMu.Lock()
	defer waMu.Unlock()
	loadWorldAlarmsLocked()
	out := make([]WorldAlarm, 0, len(waAlarms))
	for _, a := range waAlarms {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SetWorldAlarm creates or replaces an alarm. FiredFor is seeded from the
// current board so saving an alarm for something already inside its lead window
// doesn't fire it immediately — you set a 10-minute warning for a boat 3
// minutes out because you want the NEXT one.
func SetWorldAlarm(a WorldAlarm) error {
	a.Key = strings.TrimSpace(a.Key)
	if a.Key == "" {
		return fmt.Errorf("alarm needs a target")
	}
	if a.LeadMs < 0 {
		a.LeadMs = 0
	}
	a.Sound = mediaBasename(a.Sound)
	a.SpeakText = strings.TrimSpace(a.SpeakText)
	if a.Sound == "" && !a.Speak {
		// An alarm that neither plays nor speaks would be a silent no-op the
		// user believes is armed.
		return fmt.Errorf("choose a sound, text-to-speech, or both")
	}

	if t, ok := currentAlarmTargets()[a.Key]; ok && t.atMs != 0 {
		if t.kind == alarmKindOccurrence || time.Now().UnixMilli() >= t.atMs-a.LeadMs {
			a.FiredFor = t.atMs
		}
	}

	waMu.Lock()
	loadWorldAlarmsLocked()
	waAlarms[a.Key] = &a
	saveWorldAlarmsLocked()
	waMu.Unlock()
	return nil
}

// DeleteWorldAlarm removes an alarm.
func DeleteWorldAlarm(key string) {
	waMu.Lock()
	loadWorldAlarmsLocked()
	delete(waAlarms, strings.TrimSpace(key))
	saveWorldAlarmsLocked()
	waMu.Unlock()
}

// ── evaluation ──────────────────────────────────────────────────────────────

// alarmBoatDock rolls a docking instant forward past now, so an alarm always
// aims at the next arrival rather than one the cached board has already passed.
func alarmBoatDock(dockMs, periodMs, nowMs int64) int64 {
	if dockMs <= 0 {
		return 0
	}
	if periodMs > 0 {
		for dockMs <= nowMs {
			dockMs += periodMs
		}
	}
	return dockMs
}

// alarmTargetsFrom resolves every alarmable instant on a board snapshot.
func alarmTargetsFrom(d WorldTimersData, nowMs int64) map[string]alarmTarget {
	out := map[string]alarmTarget{}
	for _, e := range d.Events {
		if !e.Have || e.SpawnAtMs == 0 {
			continue
		}
		// A windowed event's actionable instant is the window OPENING —
		// SpawnAtMs is the center, and an alarm at the center is 3.6 hours
		// late to the angry goblin's camp.
		out["event:"+e.Key] = alarmTarget{atMs: e.SpawnAtMs - e.WindowMs, label: e.Name, kind: alarmKindLead}
	}
	for _, b := range d.Boats {
		if !b.Have {
			continue
		}
		if at := alarmBoatDock(b.DockAMs, b.PeriodMs, nowMs); at != 0 {
			out["boat:"+b.Key+":a"] = alarmTarget{
				atMs: at, label: b.Name + " docking at " + b.EndA, kind: alarmKindLead}
		}
		if at := alarmBoatDock(b.DockBMs, b.PeriodMs, nowMs); at != 0 {
			out["boat:"+b.Key+":b"] = alarmTarget{
				atMs: at, label: b.Name + " docking at " + b.EndB, kind: alarmKindLead}
		}
	}
	if d.LastQuakeMs != 0 {
		out[alarmQuakeKey] = alarmTarget{
			atMs: d.LastQuakeMs, label: "Earthquake", kind: alarmKindOccurrence}
	}
	return out
}

// currentAlarmTargets resolves targets from the cached board.
func currentAlarmTargets() map[string]alarmTarget {
	return alarmTargetsFrom(fetchWorldTimers(), time.Now().UnixMilli())
}

// alarmStaleGrace stops a lead alarm firing long after its moment — the app was
// asleep, or the network was down through the window. Late enough and the
// reminder is worse than none, since it describes something already over.
const alarmStaleGrace = 10 * time.Minute

// evaluateWorldAlarms fires whatever is due. Returns status lines to log.
func evaluateWorldAlarms(d WorldTimersData, now time.Time) []string {
	if !d.Enabled {
		return nil
	}
	nowMs := now.UnixMilli()
	targets := alarmTargetsFrom(d, nowMs)

	type firing struct {
		alarm WorldAlarm
		label string
	}
	var fire []firing
	var msgs []string

	waMu.Lock()
	loadWorldAlarmsLocked()
	arming := !waArmed
	waArmed = true
	dirty := false
	for key, a := range waAlarms {
		t, ok := targets[key]
		if !ok || t.atMs == 0 {
			continue
		}
		if a.FiredFor == t.atMs {
			continue // already handled this occurrence
		}
		fireAt := t.atMs - a.LeadMs
		if t.kind == alarmKindOccurrence {
			fireAt = t.atMs
		}
		if nowMs < fireAt {
			continue
		}
		// Past its moment: record it so it can't fire later, but don't sound it.
		// Same on the very first poll after launch, which is catching up rather
		// than witnessing.
		if arming || now.Sub(time.UnixMilli(fireAt)) > alarmStaleGrace {
			a.FiredFor = t.atMs
			dirty = true
			continue
		}
		label := a.Label
		if label == "" {
			label = t.label
		}
		fire = append(fire, firing{alarm: *a, label: label})
		a.FiredFor = t.atMs
		dirty = true
		if !a.Repeat {
			delete(waAlarms, key)
		}
	}
	if dirty {
		saveWorldAlarmsLocked()
	}
	waMu.Unlock()

	for _, f := range fire {
		fireWorldAlarm(f.alarm, f.label)
		msgs = append(msgs, "Alarm: "+alarmPhrase(f.alarm, f.label))
	}
	return msgs
}

// alarmPhrase is what the alarm says when it has no custom text: the thing,
// plus how long until it happens (or nothing, for a quake that just did).
func alarmPhrase(a WorldAlarm, label string) string {
	if a.SpeakText != "" {
		return a.SpeakText
	}
	if a.Key == alarmQuakeKey {
		return "Earthquake"
	}
	if a.LeadMs <= 0 {
		return label + " now"
	}
	mins := int((time.Duration(a.LeadMs) * time.Millisecond).Round(time.Minute).Minutes())
	if mins <= 0 {
		return label + " in under a minute"
	}
	if mins == 1 {
		return label + " in 1 minute"
	}
	return fmt.Sprintf("%s in %d minutes", label, mins)
}

func fireWorldAlarm(a WorldAlarm, label string) {
	if a.Sound != "" {
		if p := resolveMediaPath(a.Sound); p != "" {
			playMedia(p)
		}
	}
	if a.Speak {
		speak(alarmPhrase(a, label), false)
	}
	// Also shown, not only heard: audio can be muted or missed, and a reminder
	// that only ever existed as a sound leaves nothing to look at afterwards.
	PushWorldAlarmAlert(alarmPhrase(a, label))
}

// TestWorldAlarm plays an alarm exactly as it would fire, for the preview
// button. Sound plays regardless of the burst suppression — pressing test twice
// means you meant it twice.
func TestWorldAlarm(a WorldAlarm) {
	label := a.Label
	if label == "" {
		label = "Test"
	}
	if a.Sound != "" {
		playMediaSample(resolveMediaPath(a.Sound))
	}
	if a.Speak {
		speak(alarmPhrase(a, label), false)
	}
}

// haveWorldAlarms reports whether any alarm is configured, without touching the
// network. Cheap enough to gate the poll on.
func haveWorldAlarms() bool {
	waMu.Lock()
	defer waMu.Unlock()
	loadWorldAlarmsLocked()
	return len(waAlarms) > 0
}

// startWorldAlarmLoop polls the board and fires due alarms.
//
// The poll is skipped entirely when nothing is set, which is the common case:
// most players will never configure an alarm, and a background HTTP request
// every few seconds on their behalf is load they get nothing for. That matters
// more than it sounds — this app sits alongside a game that is periodically
// disk- and CPU-bound loading zones, and idle work it does not need to be doing
// competes with exactly that.
func startWorldAlarmLoop() {
	go func() {
		// 10s: the finest lead the UI offers is a whole minute, so firing within
		// ten seconds of the mark is well inside tolerance. It also matches the
		// Timers tab's own board poll, so with that tab open this often costs
		// nothing at all — fetchWorldTimers hands back its cache.
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			if !haveWorldAlarms() {
				continue
			}
			for _, m := range evaluateWorldAlarms(fetchWorldTimers(), time.Now()) {
				addStatus("%s", m)
			}
		}
	}()
}

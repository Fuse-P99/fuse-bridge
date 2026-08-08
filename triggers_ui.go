package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Wails bindings for the Triggers tab: live state (alert + timers + activity)
// polled by the frontend, plus the edit view's tree and mutation calls.

type TriggerAlertUI struct {
	ID       int64  `json:"id"`
	Text     string `json:"text"`
	Category string `json:"category"`
	AtMs     int64  `json:"at_ms"`
}

type TriggerTimerUI struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	StartedAtMs int64  `json:"started_at_ms"`
	EndsAtMs    int64  `json:"ends_at_ms"`
	TriggerID   int    `json:"trigger_id"`
	// Path is the owning trigger's folder chain — lets the raid card's Other
	// Timers section select bars by shared-package folder (Ring War, future
	// mob AE folders).
	Path []string `json:"path"`
}

type TriggerActivityUI struct {
	AtMs      int64    `json:"at_ms"`
	Path      []string `json:"path"`
	TriggerID int      `json:"trigger_id"`
}

type TriggerStateUI struct {
	Imported  bool                `json:"imported"`
	Character string              `json:"character"`
	Alert     *TriggerAlertUI     `json:"alert"`  // latest, for the in-tab banner
	Alerts    []TriggerAlertUI    `json:"alerts"` // recent history, oldest first
	Timers    []TriggerTimerUI    `json:"timers"`
	Activity  []TriggerActivityUI `json:"activity"`
}

// GetTriggerState returns the live trigger state. The frontend polls this ~1/s
// and animates countdown bars locally from the endsAt timestamps.
func (a *App) GetTriggerState() TriggerStateUI {
	trigStoreMu.Lock()
	imported := trigCfg != nil
	trigStoreMu.Unlock()

	trigStateMu.Lock()
	defer trigStateMu.Unlock()

	out := TriggerStateUI{
		Imported:  imported,
		Character: trigActiveChar,
		Timers:    make([]TriggerTimerUI, 0, len(liveTimers)),
		Activity:  make([]TriggerActivityUI, 0, len(trigActivity)),
	}
	out.Alerts = make([]TriggerAlertUI, 0, len(trigAlerts))
	for _, a := range trigAlerts {
		out.Alerts = append(out.Alerts, TriggerAlertUI{
			ID: a.id, Text: a.text, Category: a.category, AtMs: a.at.UnixMilli(),
		})
	}
	if n := len(out.Alerts); n > 0 {
		latest := out.Alerts[n-1]
		out.Alert = &latest
	}
	nowMs := time.Now().UnixMilli()
	for _, lt := range liveTimers {
		startedMs := lt.startedAt.UnixMilli()
		// TimerVisibleDuration: hide the bar until its last N ms, then show it as a
		// bar spanning that window (appears full and drains). The timer itself keeps
		// running and firing its end-triggers regardless.
		if lt.visibleDurMs > 0 {
			visibleFrom := lt.endsAt.UnixMilli() - lt.visibleDurMs
			if nowMs < visibleFrom {
				continue // not visible yet
			}
			if visibleFrom > startedMs {
				startedMs = visibleFrom
			}
		}
		out.Timers = append(out.Timers, TriggerTimerUI{
			ID: lt.id, Name: lt.name, Category: lt.category,
			StartedAtMs: startedMs, EndsAtMs: lt.endsAt.UnixMilli(),
			TriggerID: lt.triggerID, Path: lt.path,
		})
	}
	// Newest first for the activity feed.
	for i := len(trigActivity) - 1; i >= 0; i-- {
		e := trigActivity[i]
		out.Activity = append(out.Activity, TriggerActivityUI{
			AtMs: e.at.UnixMilli(), Path: e.path, TriggerID: e.triggerID,
		})
	}
	return out
}

// TriggerCharacterUI is one entry in the Manage Timers character picker.
type TriggerCharacterUI struct {
	Name       string `json:"name"`
	Class      string `json:"class"`
	Current    bool   `json:"current"`    // the character whose log is being tailed
	Configured bool   `json:"configured"` // has saved enable/disable overrides
}

// GetTriggerCharacters lists the characters whose triggers can be configured:
// every toon with an eqlog file on this machine, plus any that already have
// saved overrides (so a toon whose log was archived doesn't vanish along with
// its settings). Sorted with the current character first.
func (a *App) GetTriggerCharacters() []TriggerCharacterUI {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n = strings.TrimSpace(n); n == "" {
			return
		}
		if k := strings.ToLower(n); !seen[k] {
			seen[k] = true
			names = append(names, n)
		}
	}

	add(currentCharName)
	for _, n := range logFileCharNames(GetSettings().EQDirectory) {
		add(n)
	}
	trigStoreMu.Lock()
	configured := make(map[string]bool, len(trigTogglesAll))
	for k := range trigTogglesAll {
		configured[k] = true
		add(k)
	}
	trigStoreMu.Unlock()

	// Current first, then alphabetical.
	sort.Slice(names, func(i, j int) bool {
		ci := strings.EqualFold(names[i], currentCharName)
		cj := strings.EqualFold(names[j], currentCharName)
		if ci != cj {
			return ci
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	out := make([]TriggerCharacterUI, 0, len(names))
	for _, n := range names {
		out = append(out, TriggerCharacterUI{
			Name:       n,
			Class:      trigClassFor(n),
			Current:    strings.EqualFold(n, currentCharName),
			Configured: configured[strings.ToLower(n)],
		})
	}
	return out
}

// TriggerCategoryUI is one overlay-able category. A category that has both text
// alerts and countdown bars yields two entries — they're separate overlays.
type TriggerCategoryUI struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`  // "timers" | "alerts"
	Count int    `json:"count"` // triggers of this kind assigned to it
	// Enabled counts those that would actually fire for the current character
	// (same test as TriggerGroupUI.EnabledTriggers).
	Enabled int           `json:"enabled"`
	Style   CategoryStyle `json:"style"`
}

// GetTriggerCategories inventories every category the trigger set references,
// for the Manage Overlays page, plus any the user created that nothing is
// assigned to yet.
//
// Counts come from the whole tree rather than the compiled active set: a
// category whose triggers this character has switched off still needs to be
// listed, or there'd be no way to style or re-enable it.
func (a *App) GetTriggerCategories() []TriggerCategoryUI {
	type acc struct {
		name                         string
		timers, alerts               int
		timersEnabled, alertsEnabled int
	}
	byCat := map[string]*acc{}
	at := func(name string) *acc {
		k := strings.ToLower(name)
		if byCat[k] == nil {
			byCat[k] = &acc{name: name}
		}
		return byCat[k]
	}

	trigStoreMu.Lock()
	// Enabled counts are for whoever is logged in — this page configures the
	// overlays the current character sees.
	ctx := trigCtxForLocked(currentCharName, false)
	for id, t := range trigByID {
		cat := strings.TrimSpace(t.Category)
		if cat == "" {
			// Uncategorized triggers are flagged for configuration (see
			// triggerGaps) rather than silently filed under a category.
			continue
		}
		// The trigger helper folds the group chain in — and an explicit
		// trigger override beats a disabled group, so no second gate here.
		g := trigGroupOf[id]
		live := g != nil && effectiveTriggerEnabledLocked(g, t, ctx)
		e := at(cat)
		if t.TimerType == "Timer" {
			e.timers++
			if live {
				e.timersEnabled++
			}
		}
		// A timer-ended alert is a text alert too, so it belongs to the alert
		// overlay even when the trigger itself has no plain alert text.
		if (bool(t.UseText) && strings.TrimSpace(t.DisplayText) != "") ||
			(bool(t.UseTimerEnded) && t.TimerEndedTrigger != nil &&
				strings.TrimSpace(t.TimerEndedTrigger.DisplayText) != "") {
			e.alerts++
			if live {
				e.alertsEnabled++
			}
		}
	}
	trigStoreMu.Unlock()

	// User-created categories with nothing in them yet — kept visible so they
	// can be styled and popped out before triggers are assigned.
	catStyleMu.Lock()
	explicit := make(map[string]bool, len(catStyles))
	for _, s := range catStyles {
		if n := strings.TrimSpace(s.Name); n != "" {
			explicit[s.Kind+"|"+strings.ToLower(n)] = true
			at(n)
		}
	}
	catStyleMu.Unlock()

	out := make([]TriggerCategoryUI, 0, len(byCat)*2)
	for _, e := range byCat {
		for _, k := range []string{"timers", "alerts"} {
			n, on := e.timers, e.timersEnabled
			if k == "alerts" {
				n, on = e.alerts, e.alertsEnabled
			}
			if n == 0 && !explicit[k+"|"+strings.ToLower(e.name)] {
				continue
			}
			out = append(out, TriggerCategoryUI{
				Name: e.name, Kind: k, Count: n, Enabled: on,
				Style: resolveCatStyle(k, e.name),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// SyncTriggers downloads the guild's Fuse Triggers from the server (and seeds it
// if we're the first officer). Called by the frontend when the Timers window
// opens so users always have the latest shared set. Runs in the background.
// A set carrying unpublished officer edits is never overwritten by the sync.
func (a *App) SyncTriggers() { go SyncFuseTriggers() }

// PublishFuseTriggers uploads the officer's local Fuse Trigger edits as the new
// published version and returns it. Every client adopts it on their next sync.
func (a *App) PublishFuseTriggers() (int, error) { return publishFuseTriggersNow() }

// RevertFuseTriggers discards the officer's local (unpublished) Fuse edits and
// re-adopts the server's currently published copy.
func (a *App) RevertFuseTriggers() error { return revertFuseTriggersNow() }

// TriggersMeta tells the frontend whether this user is linked and an officer, so
// it can show Fuse Triggers as read-only (non-officers) and offer Personal edits.
type TriggersMeta struct {
	Linked  bool `json:"linked"`
	Officer bool `json:"officer"`
}

func (a *App) GetTriggersMeta() TriggersMeta {
	// Refresh officer status so the edit tree's editability is accurate as soon
	// as the window opens (the frontend awaits this).
	refreshOfficerStatus()
	return TriggersMeta{Linked: IsLinked(), Officer: isOfficerCached()}
}

// DismissTimer removes a single running timer from the live board (its trash
// bin). The timer will fire again the next time its trigger matches.
func (a *App) DismissTimer(id int64) { dismissTimerByID(id) }

// DismissTimerCategory clears every running timer in a category (the category
// header's trash bin).
func (a *App) DismissTimerCategory(category string) { dismissTimerCategory(category) }

// ── edit view ────────────────────────────────────────────────────────────────

// TriggerEnderUI is one early-end condition: a plain-text or regex search that,
// when it matches a later log line, ends this trigger's running timer(s) before
// they expire on their own.
type TriggerEnderUI struct {
	Text  string `json:"text"`
	Regex bool   `json:"regex"`
}

// TriggerActionUI is one set of "on this event, do these" actions: show text,
// speak it, and/or play a sound. Used for the on-match actions and for the
// timer-ending and timer-ended triggers.
type TriggerActionUI struct {
	UseText      bool   `json:"use_text"`
	DisplayText  string `json:"display_text"`
	UseTts       bool   `json:"use_tts"`
	TtsInterrupt bool   `json:"tts_interrupt"`
	TtsText      string `json:"tts_text"`
	PlayMedia    bool   `json:"play_media"`
	MediaFile    string `json:"media_file"`
}

// TriggerEditUI is a trigger reduced to the fields the app uses; it doubles as
// the payload for SaveTrigger/CreateTrigger.
type TriggerEditUI struct {
	ID          int    `json:"id"`
	GroupID     int    `json:"group_id"`
	Name        string `json:"name"`
	TriggerText string `json:"trigger_text"`
	EnableRegex bool   `json:"enable_regex"`
	// OnMatch: alert / speech / sound performed when the trigger matches.
	OnMatch TriggerActionUI `json:"on_match"`
	// Clipboard copy on match (GINA CopyToClipboard/ClipboardText).
	CopyClipboard bool   `json:"copy_clipboard"`
	ClipboardText string `json:"clipboard_text"`
	// Counter: {counter} in any text increments per match; with UseCounter on it
	// resets after CounterResetSeconds of no match (0 = never).
	UseCounter          bool   `json:"use_counter"`
	CounterResetSeconds int    `json:"counter_reset_seconds"`
	Category            string `json:"category"`
	// Timer.
	TimerEnabled        bool   `json:"timer_enabled"`
	TimerName           string `json:"timer_name"`
	TimerSeconds        int    `json:"timer_seconds"`
	TimerVisibleSeconds int    `json:"timer_visible_seconds"` // 0 = show for the whole duration
	TimerStartBehavior  string `json:"timer_start_behavior"`
	// EarlyEnders end this trigger's running timer ahead of schedule (GINA
	// TimerEarlyEnders). Only meaningful when TimerEnabled.
	EarlyEnders []TriggerEnderUI `json:"early_enders"`
	// Timer-ending (fires EndingSeconds before expiry) and timer-ended (at expiry)
	// actions. Enabled flags map to GINA UseTimerEnding/UseTimerEnded.
	EndingEnabled bool            `json:"ending_enabled"`
	EndingSeconds int             `json:"ending_seconds"`
	Ending        TriggerActionUI `json:"ending"`
	EndedEnabled  bool            `json:"ended_enabled"`
	Ended         TriggerActionUI `json:"ended"`
	// Metadata.
	Unsupported bool   `json:"unsupported"`
	Enabled     bool   `json:"enabled"`
	Incomplete  string `json:"incomplete"`
	Editable    bool   `json:"editable"`
	// Audio mute (Fuse subtree): Muted is this trigger's own flag; MutedEff
	// includes inheritance from muted ancestor groups.
	Muted    bool `json:"muted"`
	MutedEff bool `json:"muted_eff"`
	// Clipboard block (Fuse subtree), same own/effective split as the mute.
	ClipBlocked    bool `json:"clip_blocked"`
	ClipBlockedEff bool `json:"clip_blocked_eff"`
	// Defaults editor only: this trigger sits under the class-specific
	// section, so its enablement stays per-character automatic (no slider).
	ClassAuto bool `json:"class_auto,omitempty"`
}

type TriggerGroupUI struct {
	ID       int              `json:"id"`
	Name     string           `json:"name"`
	Enabled  bool             `json:"enabled"`
	Groups   []TriggerGroupUI `json:"groups"`
	Triggers []TriggerEditUI  `json:"triggers"`
	// TotalTriggers counts the whole subtree (this group + all descendants).
	TotalTriggers int `json:"total_triggers"`
	// EnabledTriggers counts those of TotalTriggers that would actually fire for
	// this character: the trigger's own toggle is on AND its immediate group is
	// enabled. That's the same test RebuildTriggerActivation applies — note it
	// walks into subgroups regardless of the parent's state, so a live subgroup
	// under a disabled parent still counts.
	EnabledTriggers int `json:"enabled_triggers"`
	// Editable: officers can edit Fuse groups; everyone can edit Personal.
	Editable bool `json:"editable"`
	// Personal marks the user-owned subtree (vs the server-synced Fuse set).
	Personal bool `json:"personal"`
	// Audio mute (Fuse subtree): Muted is this group's own flag; MutedEff
	// includes inheritance from muted ancestors.
	Muted    bool `json:"muted"`
	MutedEff bool `json:"muted_eff"`
	// Clipboard block (Fuse subtree), same own/effective split as the mute.
	ClipBlocked    bool `json:"clip_blocked"`
	ClipBlockedEff bool `json:"clip_blocked_eff"`
	// Uniform category: when every categorized trigger in this group's subtree
	// shares ONE category, the UI offers group-level popout shortcuts to that
	// category's overlays. UniformTimers/UniformAlerts say which overlay kinds
	// the subtree actually feeds. Empty when the subtree spans categories (the
	// per-trigger buttons cover that case).
	UniformCategory string `json:"uniform_category,omitempty"`
	UniformTimers   bool   `json:"uniform_timers,omitempty"`
	UniformAlerts   bool   `json:"uniform_alerts,omitempty"`
	// Defaults editor only: ClassNote marks the class-specific section row
	// ("Class Auto-detected" note, own slider kept); ClassAuto marks
	// everything beneath it, whose enablement stays per-character automatic —
	// the UI shows "auto" instead of a slider.
	ClassNote bool `json:"class_note,omitempty"`
	ClassAuto bool `json:"class_auto,omitempty"`
	// Set on the Fuse Triggers ROOT only: the published version this local copy
	// is based on (shown as "Fuse Triggers (v35)"), the newest version seen on
	// the server, and whether local officer edits are awaiting publish.
	Version       int  `json:"version,omitempty"`
	ServerVersion int  `json:"server_version,omitempty"`
	Dirty         bool `json:"dirty,omitempty"`
}

// actionFromEnd maps a GINA end-trigger to the UI action set (bare media name).
func actionFromEnd(et *GinaEndTrigger) TriggerActionUI {
	if et == nil {
		return TriggerActionUI{}
	}
	return TriggerActionUI{
		UseText:      bool(et.UseText),
		DisplayText:  et.DisplayText,
		UseTts:       bool(et.UseTextToVoice),
		TtsInterrupt: bool(et.InterruptSpeech),
		TtsText:      et.TextToVoiceText,
		PlayMedia:    bool(et.PlayMediaFile),
		MediaFile:    mediaBasename(et.MediaFileName),
	}
}

func triggerToUI(t *GinaTrigger, g *GinaGroup, editable bool, ctx trigEnableCtx) TriggerEditUI {
	durMs := t.TimerMillisecondDuration
	if durMs <= 0 {
		durMs = int64(t.TimerDuration) * 1000
	}
	var enders []TriggerEnderUI
	if t.TimerEarlyEnders != nil {
		for _, e := range t.TimerEarlyEnders.Enders {
			enders = append(enders, TriggerEnderUI{Text: e.EarlyEndText, Regex: bool(e.EnableRegex)})
		}
	}
	// The retired RestartBasedOnTimerName flag folds into RestartTimer, so an
	// imported trigger that used it shows (and saves as) "restart" rather than a
	// dropdown value the engine would silently override. See applyTriggerEdit.
	startBehavior := t.TimerStartBehavior
	if bool(t.RestartBasedOnTimerName) {
		startBehavior = "RestartTimer"
	}
	endingSeconds := t.TimerEndingTime
	if endingSeconds <= 0 {
		endingSeconds = 1
	}
	return TriggerEditUI{
		ID:          t.ID,
		GroupID:     g.GroupID,
		Name:        t.Name,
		TriggerText: t.TriggerText,
		EnableRegex: bool(t.EnableRegex),
		OnMatch: TriggerActionUI{
			UseText:      bool(t.UseText),
			DisplayText:  t.DisplayText,
			UseTts:       bool(t.UseTextToVoice),
			TtsInterrupt: bool(t.InterruptSpeech),
			TtsText:      t.TextToVoiceText,
			PlayMedia:    bool(t.PlayMediaFile),
			MediaFile:    mediaBasename(t.MediaFileName),
		},
		CopyClipboard:       bool(t.CopyToClipboard),
		ClipboardText:       t.ClipboardText,
		UseCounter:          bool(t.UseCounterResetTimer),
		CounterResetSeconds: t.CounterResetDuration,
		Category:            t.Category,
		TimerEnabled:        t.TimerType == "Timer",
		TimerName:           t.TimerName,
		TimerSeconds:        int(durMs / 1000),
		TimerVisibleSeconds: t.TimerVisibleDuration,
		TimerStartBehavior:  startBehavior,
		EarlyEnders:         enders,
		EndingEnabled:       bool(t.UseTimerEnding),
		EndingSeconds:       endingSeconds,
		Ending:              actionFromEnd(t.TimerEndingTrigger),
		EndedEnabled:        bool(t.UseTimerEnded),
		Ended:               actionFromEnd(t.TimerEndedTrigger),
		Unsupported:         !patternSupported(t.ID, t.TriggerText, bool(t.EnableRegex)),
		Enabled:             effectiveTriggerEnabledLocked(g, t, ctx),
		Incomplete:          triggerGaps(t),
		Editable:            editable,
	}
}

// triggerShowsAlert reports whether a trigger produces visible alert text —
// on match, at the timer-ending warning, or at expiry. Mirrors the client's
// per-trigger "Alerts" popout-button test.
func triggerShowsAlert(t *GinaTrigger) bool {
	if bool(t.UseText) && strings.TrimSpace(t.DisplayText) != "" {
		return true
	}
	if bool(t.UseTimerEnding) && t.TimerEndingTrigger != nil &&
		bool(t.TimerEndingTrigger.UseText) && strings.TrimSpace(t.TimerEndingTrigger.DisplayText) != "" {
		return true
	}
	if bool(t.UseTimerEnded) && t.TimerEndedTrigger != nil &&
		bool(t.TimerEndedTrigger.UseText) && strings.TrimSpace(t.TimerEndedTrigger.DisplayText) != "" {
		return true
	}
	return false
}

// triggerGaps reports why a trigger would produce nothing, or "" when it's fully
// configured. A trigger that matches but shows no alert, plays no sound, speaks
// nothing and starts no timer is a no-op. A visual action (alert/timer) also
// needs a category to route to an overlay; a sound/speech-only trigger doesn't.
func triggerGaps(t *GinaTrigger) string {
	timer := t.TimerType == "Timer"
	alert := bool(t.UseText) && strings.TrimSpace(t.DisplayText) != ""
	ended := bool(t.UseTimerEnded) && t.TimerEndedTrigger != nil &&
		strings.TrimSpace(t.TimerEndedTrigger.DisplayText) != ""
	tts := bool(t.UseTextToVoice) &&
		(strings.TrimSpace(t.TextToVoiceText) != "" || strings.TrimSpace(t.DisplayText) != "")
	media := bool(t.PlayMediaFile) && strings.TrimSpace(t.MediaFileName) != ""
	clip := bool(t.CopyToClipboard) && strings.TrimSpace(t.ClipboardText) != ""

	var why []string
	if !timer && !alert && !ended && !tts && !media && !clip {
		why = append(why, "shows no alert, plays no sound, and starts no timer")
	}
	if (timer || alert || ended) && strings.TrimSpace(t.Category) == "" {
		why = append(why, "has no category, so it can't be shown as an overlay")
	}
	if len(why) == 0 {
		return ""
	}
	return "Needs configuration: this trigger " + strings.Join(why, ", and ") + "."
}

// GetTriggerTree returns the hierarchy with enablement for the character whose
// log is being tailed.
func (a *App) GetTriggerTree() []TriggerGroupUI {
	return a.GetTriggerTreeFor(currentCharName)
}

// GetTriggerTreeFor returns the hierarchy with enablement evaluated for a
// specific character, so Manage Timers can show and edit any toon's set. Passing
// "" means the current character. Read-only: no bucket is created for a
// character just because it was viewed.
func (a *App) GetTriggerTreeFor(charName string) []TriggerGroupUI {
	if strings.TrimSpace(charName) == "" {
		charName = currentCharName
	}
	trigStoreMu.Lock()
	defer trigStoreMu.Unlock()
	if trigCfg == nil {
		return []TriggerGroupUI{}
	}
	ctx := trigCtxForLocked(charName, false)
	officer := isOfficerCached()
	// The defaults editor shows the class-specific section as auto-detected.
	defaultsView := strings.EqualFold(strings.TrimSpace(charName), trigDefaultsChar)
	// catAgg accumulates a subtree's category spread and overlay kinds, so a
	// group whose triggers all share one category can offer group-level
	// popout shortcuts.
	type catAgg struct {
		cats           map[string]bool
		timers, alerts bool
	}
	var conv func(g *GinaGroup, mutedInherit, clipInherit, classAuto bool) (TriggerGroupUI, catAgg)
	conv = func(g *GinaGroup, mutedInherit, clipInherit, classAuto bool) (TriggerGroupUI, catAgg) {
		personal := isPersonalGroupLocked(g.GroupID)
		editable := personal || officer
		mutedSelf := trigMuteGroups[g.GroupID]
		mutedEff := mutedInherit || mutedSelf
		clipSelf := trigClipGroups[g.GroupID]
		clipEff := clipInherit || clipSelf
		classNote := false
		if defaultsView && !personal && sectionIsClassSpecific(g.Name) {
			if p := groupParentOf[g.GroupID]; p != nil && p.GroupID == fuseRootGroupID {
				classNote = true
			}
		}
		childAuto := classAuto || classNote
		agg := catAgg{cats: map[string]bool{}}
		ug := TriggerGroupUI{
			ID:             g.GroupID,
			Name:           g.Name,
			Enabled:        effectiveGroupEnabledLocked(g, ctx),
			Groups:         make([]TriggerGroupUI, 0, len(g.Groups)),
			Triggers:       make([]TriggerEditUI, 0, len(g.Triggers)),
			Editable:       editable,
			Personal:       personal,
			Muted:          mutedSelf,
			MutedEff:       mutedEff,
			ClipBlocked:    clipSelf,
			ClipBlockedEff: clipEff,
			ClassNote:      classNote,
			ClassAuto:      classAuto,
		}
		// The Fuse root carries the revision info every user sees ("v35") and
		// the officer publish bar reads (dirty / newer server version).
		if g.GroupID == fuseRootGroupID {
			ug.Version = fuseVersion
			ug.ServerVersion = fuseServerVersion
			ug.Dirty = fuseDirty
		}
		for _, t := range g.Triggers {
			tu := triggerToUI(t, g, editable, ctx)
			key := trigToggleKey(g, t)
			tu.Muted = trigMuteTriggers[key]
			tu.MutedEff = mutedEff || tu.Muted
			tu.ClipBlocked = trigClipTriggers[key]
			tu.ClipBlockedEff = clipEff || tu.ClipBlocked
			tu.ClassAuto = childAuto
			// tu.Enabled IS the firing answer now — it folds the group chain
			// in, and an explicit trigger override beats a disabled group. A
			// second ug.Enabled gate here would hide exactly those overrides
			// from the tally.
			if tu.Enabled {
				ug.EnabledTriggers++
			}
			// Category spread: uncategorized triggers don't break uniformity
			// (they have no overlay to open either way).
			if c := strings.TrimSpace(t.Category); c != "" {
				agg.cats[strings.ToLower(c)] = true
				// Remember a display-cased name for the popout call.
				if len(agg.cats) == 1 {
					ug.UniformCategory = c
				}
				if t.TimerType == "Timer" {
					agg.timers = true
				}
				if triggerShowsAlert(t) {
					agg.alerts = true
				}
			}
			ug.Triggers = append(ug.Triggers, tu)
		}
		ug.TotalTriggers = len(ug.Triggers)
		for _, c := range g.Groups {
			cu, ca := conv(c, mutedEff, clipEff, childAuto)
			ug.TotalTriggers += cu.TotalTriggers
			ug.EnabledTriggers += cu.EnabledTriggers
			for k := range ca.cats {
				agg.cats[k] = true
			}
			if len(agg.cats) == 1 && ug.UniformCategory == "" {
				ug.UniformCategory = cu.UniformCategory
			}
			agg.timers = agg.timers || ca.timers
			agg.alerts = agg.alerts || ca.alerts
			// Hide GINA groups with no triggers anywhere beneath them —
			// vestigial import leftovers (e.g. empty "On"/"Off" shells).
			// App-created groups stay visible so New Group is usable.
			if cu.TotalTriggers == 0 && cu.ID <= trigLocalGroupIDBase {
				continue
			}
			ug.Groups = append(ug.Groups, cu)
		}
		// Uniform only when exactly one category exists in the subtree.
		if len(agg.cats) == 1 {
			ug.UniformTimers = agg.timers
			ug.UniformAlerts = agg.alerts
		} else {
			ug.UniformCategory = ""
		}
		sortTriggerGroups(ug.Groups)
		return ug, agg
	}
	out := make([]TriggerGroupUI, 0, len(trigCfg.Groups))
	for _, g := range trigCfg.Groups {
		gu, _ := conv(g, false, false, false)
		if gu.TotalTriggers == 0 && gu.ID <= trigLocalGroupIDBase {
			continue
		}
		out = append(out, gu)
	}
	sortTriggerGroups(out)
	return out
}

// sortTriggerGroups orders sibling groups alphabetically (display only — the
// stored XML keeps GINA's order). Group names are prefixed ("01 - ...") to
// enforce an intended order, which a plain case-insensitive sort honors.
func sortTriggerGroups(groups []TriggerGroupUI) {
	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
}

// applyEndAction writes a UI action set onto a GINA end-trigger, allocating it
// when there's something to store and clearing it to nil when it's fully empty
// so the XML stays tidy.
func applyEndAction(dst **GinaEndTrigger, in TriggerActionUI) {
	empty := !in.UseText && !in.UseTts && !in.PlayMedia
	if empty {
		*dst = nil
		return
	}
	if *dst == nil {
		*dst = &GinaEndTrigger{}
	}
	et := *dst
	et.UseText = ginaBool(in.UseText)
	et.DisplayText = in.DisplayText
	et.UseTextToVoice = ginaBool(in.UseTts)
	et.InterruptSpeech = ginaBool(in.TtsInterrupt)
	et.TextToVoiceText = in.TtsText
	et.PlayMediaFile = ginaBool(in.PlayMedia)
	et.MediaFileName = mediaBasename(in.MediaFile)
}

// applyTriggerEdit writes the editable fields onto a GinaTrigger.
func applyTriggerEdit(t *GinaTrigger, in TriggerEditUI) {
	t.Name = strings.TrimSpace(in.Name)
	t.TriggerText = in.TriggerText
	t.EnableRegex = ginaBool(in.EnableRegex)
	// On-match alert / TTS / sound.
	t.UseText = ginaBool(in.OnMatch.UseText)
	t.DisplayText = in.OnMatch.DisplayText
	t.UseTextToVoice = ginaBool(in.OnMatch.UseTts)
	t.InterruptSpeech = ginaBool(in.OnMatch.TtsInterrupt)
	t.TextToVoiceText = in.OnMatch.TtsText
	t.PlayMediaFile = ginaBool(in.OnMatch.PlayMedia)
	t.MediaFileName = mediaBasename(in.OnMatch.MediaFile)
	// Clipboard + counter.
	t.CopyToClipboard = ginaBool(in.CopyClipboard)
	t.ClipboardText = in.ClipboardText
	t.UseCounterResetTimer = ginaBool(in.UseCounter)
	if in.CounterResetSeconds < 0 {
		in.CounterResetSeconds = 0
	}
	t.CounterResetDuration = in.CounterResetSeconds
	// Timer.
	if in.TimerEnabled {
		t.TimerType = "Timer"
	} else if t.TimerType == "Timer" {
		t.TimerType = "NoTimer"
	}
	t.TimerName = in.TimerName
	if in.TimerSeconds < 0 {
		in.TimerSeconds = 0
	}
	t.TimerDuration = in.TimerSeconds
	t.TimerMillisecondDuration = int64(in.TimerSeconds) * 1000
	if in.TimerVisibleSeconds < 0 {
		in.TimerVisibleSeconds = 0
	}
	t.TimerVisibleDuration = in.TimerVisibleSeconds
	// The old RestartBasedOnTimerName control is retired: triggerToUI already
	// folded it into TimerStartBehavior, so clear the flag here to keep it from
	// silently overriding the dropdown the user now sees.
	t.RestartBasedOnTimerName = false
	if in.TimerStartBehavior != "" {
		t.TimerStartBehavior = in.TimerStartBehavior
	}
	// Timer-ending (with its seconds-before-end) and timer-ended actions.
	t.UseTimerEnding = ginaBool(in.EndingEnabled)
	if in.EndingSeconds < 1 {
		in.EndingSeconds = 1
	}
	t.TimerEndingTime = in.EndingSeconds
	applyEndAction(&t.TimerEndingTrigger, in.Ending)
	t.UseTimerEnded = ginaBool(in.EndedEnabled)
	applyEndAction(&t.TimerEndedTrigger, in.Ended)
	// Early-end conditions: replace the set wholesale from the form. Blank rows
	// are dropped — an empty search would match every line and end the timer
	// instantly. Cleared to nil when none remain so the XML omits the element.
	var enders []GinaEarlyEnder
	for _, e := range in.EarlyEnders {
		if strings.TrimSpace(e.Text) == "" {
			continue
		}
		enders = append(enders, GinaEarlyEnder{EarlyEndText: e.Text, EnableRegex: ginaBool(e.Regex)})
	}
	if len(enders) > 0 {
		t.TimerEarlyEnders = &GinaEarlyEnders{Enders: enders}
	} else {
		t.TimerEarlyEnders = nil
	}
	t.Category = strings.TrimSpace(in.Category)
	t.Modified = time.Now().Format("2006-01-02T15:04:05")
}

// checkFuseEditLocked reports whether groupID is in the Fuse subtree and, if so,
// rejects the edit for non-officers. Caller holds trigStoreMu. When fuse is
// true and err is nil, the caller must markFuseDirty() after saving — the edit
// stays local until the officer publishes it (PublishFuseTriggers).
func checkFuseEditLocked(groupID int) (fuse bool, err error) {
	if !isFuseGroupLocked(groupID) {
		return false, nil
	}
	if !isOfficerCached() {
		return true, fmt.Errorf("only officers can edit Fuse Triggers")
	}
	return true, nil
}

// SaveTrigger updates an existing trigger and persists + reactivates.
func (a *App) SaveTrigger(in TriggerEditUI) error {
	trigStoreMu.Lock()
	t, ok := trigByID[in.ID]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
	}
	fuse, err := checkFuseEditLocked(trigGroupOf[t.ID].GroupID)
	if err != nil {
		trigStoreMu.Unlock()
		return err
	}
	if strings.TrimSpace(in.Name) == "" {
		trigStoreMu.Unlock()
		return fmt.Errorf("name is required")
	}
	// A rename changes the trigger's toggle key — carry the slider state over.
	oldKey := trigToggleKey(trigGroupOf[t.ID], t)
	applyTriggerEdit(t, in)
	newKey := trigToggleKey(trigGroupOf[t.ID], t)
	if oldKey != newKey {
		moved := false
		forEachTrigToggleSetLocked(func(s *trigToggleSet) {
			if v, ok := s.Triggers[oldKey]; ok {
				delete(s.Triggers, oldKey)
				s.Triggers[newKey] = v
				moved = true
			}
		})
		if moved {
			_ = saveTrigTogglesLocked()
		}
	}
	err = saveTriggersLocked()
	trigStoreMu.Unlock()
	invalidateTrigSupport(in.ID)
	if fuse {
		markFuseDirty()
	}
	RebuildTriggerActivation()
	return err
}

// CreateTrigger adds a trigger to the given group and returns its session id.
func (a *App) CreateTrigger(groupID int, in TriggerEditUI) (int, error) {
	trigStoreMu.Lock()
	g, ok := groupByID[groupID]
	if !ok {
		trigStoreMu.Unlock()
		return 0, fmt.Errorf("group not found")
	}
	fuse, err := checkFuseEditLocked(groupID)
	if err != nil {
		trigStoreMu.Unlock()
		return 0, err
	}
	if strings.TrimSpace(in.Name) == "" {
		trigStoreMu.Unlock()
		return 0, fmt.Errorf("name is required")
	}
	t := &GinaTrigger{
		TimerType:          "NoTimer",
		TimerStartBehavior: "StartNewTimer",
		TimerEndingTime:    1,
		UseFastCheck:       true,
		Category:           "Default",
	}
	applyTriggerEdit(t, in)
	// A new trigger never starts uncategorized: an empty pick means "Default".
	// The category itself needs no separate creation — it appears in Manage
	// Overlays (with a default style) as soon as a trigger references it.
	if t.Category == "" {
		t.Category = "Default"
	}
	trigNextID++
	t.ID = trigNextID
	g.Triggers = append(g.Triggers, t)
	trigByID[t.ID] = t
	trigGroupOf[t.ID] = g
	err = saveTriggersLocked()
	id := t.ID
	trigStoreMu.Unlock()
	if fuse {
		markFuseDirty()
	}
	RebuildTriggerActivation()
	return id, err
}

// DeleteTrigger removes a trigger and persists + reactivates.
func (a *App) DeleteTrigger(id int) error {
	trigStoreMu.Lock()
	g, ok := trigGroupOf[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
	}
	fuse, err := checkFuseEditLocked(g.GroupID)
	if err != nil {
		trigStoreMu.Unlock()
		return err
	}
	for i, t := range g.Triggers {
		if t.ID == id {
			g.Triggers = append(g.Triggers[:i], g.Triggers[i+1:]...)
			break
		}
	}
	delete(trigByID, id)
	delete(trigGroupOf, id)
	err = saveTriggersLocked()
	trigStoreMu.Unlock()
	if fuse {
		markFuseDirty()
	}
	RebuildTriggerActivation()
	return err
}

// CreateTriggerGroup adds a group under parentID. Top-level creation (parentID
// 0) isn't allowed — new groups go under Fuse Triggers (officers) or Personal.
func (a *App) CreateTriggerGroup(parentID int, name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("name is required")
	}
	trigStoreMu.Lock()
	if trigCfg == nil {
		trigStoreMu.Unlock()
		return 0, fmt.Errorf("no triggers loaded")
	}
	if parentID == 0 {
		trigStoreMu.Unlock()
		return 0, fmt.Errorf("choose a parent group (Fuse Triggers or Personal)")
	}
	p, ok := groupByID[parentID]
	if !ok {
		trigStoreMu.Unlock()
		return 0, fmt.Errorf("parent group not found")
	}
	fuse, err := checkFuseEditLocked(parentID)
	if err != nil {
		trigStoreMu.Unlock()
		return 0, err
	}
	g := &GinaGroup{Name: name, GroupID: nextGroupIDLocked()}
	p.Groups = append(p.Groups, g)
	groupParentOf[g.GroupID] = p
	groupByID[g.GroupID] = g
	err = saveTriggersLocked()
	id := g.GroupID
	trigStoreMu.Unlock()
	if fuse {
		markFuseDirty()
	}
	return id, err
}

// SetTriggerGroupEnabled flips a group's on/off slider and cascades it through
// the whole subtree: every nested subgroup AND every trigger inside gets the
// same state (stored as local overrides on top of GINA's per-character
// enablement), so the switch visibly turns everything under it on or off.
func (a *App) SetTriggerGroupEnabled(id int, enabled bool) error {
	return a.SetTriggerGroupEnabledFor("", id, enabled)
}

// SetTriggerGroupEnabledFor is SetTriggerGroupEnabled for a specific character
// ("" = the current one), backing the character picker in Manage Timers.
func (a *App) SetTriggerGroupEnabledFor(charName string, id int, enabled bool) error {
	trigStoreMu.Lock()
	g, ok := groupByID[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("group not found")
	}
	set := trigToggleSetForLocked(resolveToggleChar(charName), true)
	// inClass: the cascade has crossed INTO a class-specific section's
	// content. Enabling there must not stamp explicit trues — that would
	// blanket every class for whoever the set applies to (the defaults
	// editor's own strip only ran at save, so even the editor showed all
	// classes on). Clearing the overrides instead restores auto: the walk's
	// boundary rule turns the section's ON into "the detected class only".
	// Disabling still stamps false — off means off, every class.
	// A click directly ON a class subsection starts below the boundary and
	// stamps normally: reaching in deliberately keeps working.
	var walk func(g *GinaGroup, inClass bool)
	walk = func(g *GinaGroup, inClass bool) {
		if inClass && enabled {
			delete(set.Groups, g.GroupID)
			for _, t := range g.Triggers {
				delete(set.Triggers, trigToggleKey(g, t))
			}
		} else {
			set.Groups[g.GroupID] = enabled
			for _, t := range g.Triggers {
				set.Triggers[trigToggleKey(g, t)] = enabled
			}
		}
		in := inClass || sectionIsClassSpecific(g.Name)
		for _, c := range g.Groups {
			walk(c, in)
		}
	}
	walk(g, false)
	err := saveTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

// SetTriggerEnabled flips a single trigger's on/off slider and reactivates.
func (a *App) SetTriggerEnabled(id int, enabled bool) error {
	return a.SetTriggerEnabledFor("", id, enabled)
}

// SetTriggerEnabledFor is SetTriggerEnabled for a specific character.
func (a *App) SetTriggerEnabledFor(charName string, id int, enabled bool) error {
	trigStoreMu.Lock()
	t, ok := trigByID[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
	}
	set := trigToggleSetForLocked(resolveToggleChar(charName), true)
	set.Triggers[trigToggleKey(trigGroupOf[id], t)] = enabled
	err := saveTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

// ── Configure Defaults: one enablement set applied to every character ───────

// BeginTriggerDefaults opens a staging set for the defaults editor. The
// tree/toggle bindings reach it through the reserved trigDefaultsChar name;
// nothing real changes until SaveTriggerDefaults.
//
// The staging starts as a CLONE of the current saved defaults (the seed —
// exactly what SaveTriggerDefaults wrote last time), not a blank slate: the
// editor must open on what a fresh character would get today. Starting empty
// silently threw the previous session's choices away and showed the
// rule-based defaults as if nothing had ever been saved.
func (a *App) BeginTriggerDefaults() {
	trigStoreMu.Lock()
	if trigToggleSeed != nil {
		trigDefaultsStaging = trigToggleSeed.clone()
	} else {
		trigDefaultsStaging = newTrigToggleSet()
	}
	trigStoreMu.Unlock()
}

// CancelTriggerDefaults discards the staging set.
func (a *App) CancelTriggerDefaults() {
	trigStoreMu.Lock()
	trigDefaultsStaging = nil
	trigStoreMu.Unlock()
}

// SaveTriggerDefaults applies the staged enablement to the seed (future
// characters) and EVERY existing character's set, overwriting their current
// choices — the UI confirms before calling. Class-specific folders are
// stripped from the staging first (a group cascade writes the whole subtree,
// and those folders must stay auto-detected per character; the section's own
// toggle is kept). Returns how many characters were overwritten.
func (a *App) SaveTriggerDefaults() (int, error) {
	trigStoreMu.Lock()
	if trigDefaultsStaging == nil {
		trigStoreMu.Unlock()
		return 0, fmt.Errorf("not editing defaults")
	}
	// Strip everything beneath class-specific sections of the Fuse set.
	if trigCfg != nil {
		var strip func(g *GinaGroup)
		strip = func(g *GinaGroup) {
			delete(trigDefaultsStaging.Groups, g.GroupID)
			for _, t := range g.Triggers {
				delete(trigDefaultsStaging.Triggers, trigToggleKey(g, t))
			}
			for _, c := range g.Groups {
				strip(c)
			}
		}
		for _, root := range trigCfg.Groups {
			if root.GroupID != fuseRootGroupID {
				continue
			}
			for _, sec := range root.Groups {
				if !sectionIsClassSpecific(sec.Name) {
					continue
				}
				for _, c := range sec.Groups {
					strip(c)
				}
			}
		}
	}
	applied := 0
	for key := range trigTogglesAll {
		trigTogglesAll[key] = trigDefaultsStaging.clone()
		applied++
	}
	trigToggleSeed = trigDefaultsStaging.clone()
	trigDefaultsStaging = nil
	err := saveTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	emitTriggersChanged()
	return applied, err
}

// resolveToggleChar maps an empty character name to the tailed character, so the
// no-argument bindings keep editing whoever is logged in.
func resolveToggleChar(charName string) string {
	if strings.TrimSpace(charName) == "" {
		return currentCharName
	}
	return charName
}

// RenameTriggerGroup renames a group and persists.
func (a *App) RenameTriggerGroup(id int, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if id == fuseRootGroupID || id == personalRootGroupID {
		return fmt.Errorf("this group can't be renamed")
	}
	trigStoreMu.Lock()
	g, ok := groupByID[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("group not found")
	}
	fuse, err := checkFuseEditLocked(id)
	if err != nil {
		trigStoreMu.Unlock()
		return err
	}
	g.Name = name
	err = saveTriggersLocked()
	trigStoreMu.Unlock()
	if fuse {
		markFuseDirty()
	}
	return err
}

// DeleteTriggerGroup removes a group and its entire subtree (subgroups and
// triggers), persists, and reactivates.
func (a *App) DeleteTriggerGroup(id int) error {
	if id == fuseRootGroupID || id == personalRootGroupID {
		return fmt.Errorf("this group can't be deleted")
	}
	trigStoreMu.Lock()
	g, ok := groupByID[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("group not found")
	}
	fuse, err := checkFuseEditLocked(id)
	if err != nil {
		trigStoreMu.Unlock()
		return err
	}
	// Detach from the parent (or the root list).
	if p := groupParentOf[id]; p != nil {
		for i, c := range p.Groups {
			if c == g {
				p.Groups = append(p.Groups[:i], p.Groups[i+1:]...)
				break
			}
		}
	} else {
		for i, c := range trigCfg.Groups {
			if c == g {
				trigCfg.Groups = append(trigCfg.Groups[:i], trigCfg.Groups[i+1:]...)
				break
			}
		}
	}
	// Drop the subtree from the indexes and clean up its slider overrides so a
	// future group reusing an id/name doesn't inherit stale state.
	var scrub func(g *GinaGroup)
	scrub = func(g *GinaGroup) {
		delete(groupByID, g.GroupID)
		delete(groupParentOf, g.GroupID)
		forEachTrigToggleSetLocked(func(s *trigToggleSet) { delete(s.Groups, g.GroupID) })
		for _, t := range g.Triggers {
			delete(trigByID, t.ID)
			delete(trigGroupOf, t.ID)
			key := trigToggleKey(g, t)
			forEachTrigToggleSetLocked(func(s *trigToggleSet) { delete(s.Triggers, key) })
		}
		for _, c := range g.Groups {
			scrub(c)
		}
	}
	scrub(g)
	_ = saveTrigTogglesLocked()
	err = saveTriggersLocked()
	trigStoreMu.Unlock()
	if fuse {
		markFuseDirty()
	}
	RebuildTriggerActivation()
	return err
}

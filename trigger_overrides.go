package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Personal customization for the Fuse trigger package (and per-trigger color
// for any trigger, Personal included).
//
// Members can't edit Fuse triggers, so before this layer their only levers
// were the enable toggles, the audio mute, and the clipboard block. Overrides
// complete the set: swap or silence a trigger's sound and speech, reword or
// hide its alert text, re-assign which overlay it feeds, and tint its bars
// and alert text — all locally, without touching the package XML. An officer
// republish replaces the package wholesale (syncFuseTriggersXML), which is
// exactly why customization must live beside the package rather than in it.
//
// Ground rules, enforced at save (normalizeOverride) and again at activation
// build (defFromTrigger), so a stale entry can never do more than the current
// package allows:
//   - Alert TEXT overrides reword or suppress only: they apply only while the
//     package actually shows text in that slot. What appears on the guild's
//     alert overlays stays officer-decided; members can quiet or reword it.
//   - TTS and SOUND overrides may add as well as replace or silence — audio
//     is purely local, and "give this silent raid call a sound" is a core
//     member ask.
//   - The timer-ending/ended slots apply only while the package has that slot
//     armed: a slot the officers never enabled has nothing to fire from.
//     Within an armed ending slot the warning TIME is adjustable too, and the
//     per-slot speech-interrupt flag and the "if already running" behavior
//     are personal preference — none of them change what the trigger detects
//     or how long its timer runs.
//
// Stored app-wide (not per character), like the mutes and clipboard blocks:
// what a trigger sounds and looks like on this PC is a machine preference,
// not a per-toon loadout. Keyed by the stable "GroupID/Name" (trigToggleKey)
// so overrides survive a package republish; session trigger ids don't.
// Guarded by trigStoreMu alongside the tree they describe.

// TriggerOverride is one trigger's local customization, layered over the
// package/personal definition at activation-build time. Pointer semantics on
// every field: nil = "no override, use the package value"; a non-nil empty
// string = "explicitly none" (silent / hidden / no overlay).
type TriggerOverride struct {
	// Overlay re-assigns which overlay (GinaTrigger.Category internally) this
	// trigger feeds. "" = deliberately unassigned (flagged as needing setup,
	// same as an unassigned package trigger).
	Overlay *string `json:"overlay,omitempty"`
	// Color tints this one trigger inside its overlays — its timer-bar fill
	// and its alert text. nil = the overlay's own color.
	Color *string `json:"color,omitempty"`
	// TimerName renames what this trigger's countdown bar displays. Only
	// meaningful for a timer. nil = the package name; there is no "no name" —
	// the engine falls back to the trigger name when the template is empty.
	TimerName *string `json:"timer_name,omitempty"`
	// StartBehavior overrides "if already running" (TimerStartBehavior):
	// StartNewTimer / RestartTimer / IgnoreIfRunning. nil = the package's.
	StartBehavior *string `json:"start_behavior,omitempty"`
	// On-match presentation.
	Text  *string `json:"text,omitempty"`
	TTS   *string `json:"tts,omitempty"`
	Sound *string `json:"sound,omitempty"`
	// Interrupt overrides whether this trigger's speech cuts off any speech
	// already playing. nil = the package flag; one flag per speech slot.
	Interrupt *bool `json:"interrupt,omitempty"`
	// Timer-ending slot (the warning before expiry). EndingSeconds moves when
	// the warning fires; it applies only while the package has the slot armed
	// (members can re-time an existing warning, not conjure one).
	EndingSeconds   *int    `json:"ending_seconds,omitempty"`
	EndingText      *string `json:"ending_text,omitempty"`
	EndingTTS       *string `json:"ending_tts,omitempty"`
	EndingSound     *string `json:"ending_sound,omitempty"`
	EndingInterrupt *bool   `json:"ending_interrupt,omitempty"`
	// Timer-ended slot (at expiry).
	EndedText      *string `json:"ended_text,omitempty"`
	EndedTTS       *string `json:"ended_tts,omitempty"`
	EndedSound     *string `json:"ended_sound,omitempty"`
	EndedInterrupt *bool   `json:"ended_interrupt,omitempty"`
}

func (o *TriggerOverride) empty() bool {
	return o == nil || (o.Overlay == nil && o.Color == nil && o.TimerName == nil &&
		o.StartBehavior == nil &&
		o.Text == nil && o.TTS == nil && o.Sound == nil && o.Interrupt == nil &&
		o.EndingSeconds == nil &&
		o.EndingText == nil && o.EndingTTS == nil && o.EndingSound == nil &&
		o.EndingInterrupt == nil &&
		o.EndedText == nil && o.EndedTTS == nil && o.EndedSound == nil &&
		o.EndedInterrupt == nil)
}

// trigOverrides: trigKey ("GroupID/Name") → customization. Under trigStoreMu.
var trigOverrides = map[string]*TriggerOverride{}

type trigOverridesFile struct {
	Triggers map[string]*TriggerOverride `json:"triggers"`
}

func trigOverridesPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "trigger_overrides.json")
}

// loadTrigOverridesLocked loads the persisted overrides. Caller holds
// trigStoreMu.
func loadTrigOverridesLocked() {
	trigOverrides = map[string]*TriggerOverride{}
	data, err := os.ReadFile(trigOverridesPath())
	if err != nil {
		return
	}
	var f trigOverridesFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for k, ov := range f.Triggers {
		if !ov.empty() {
			trigOverrides[k] = ov
		}
	}
}

// saveTrigOverridesLocked persists the overrides. Caller holds trigStoreMu.
func saveTrigOverridesLocked() error {
	f := trigOverridesFile{Triggers: map[string]*TriggerOverride{}}
	for k, ov := range trigOverrides {
		if !ov.empty() {
			f.Triggers[k] = ov
		}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(trigOverridesPath(), data)
}

// effAlertTexts returns a trigger's EFFECTIVE alert texts (on-match, ending,
// ended) with the override layer applied — the same slot-armed and
// reword-only rules defFromTrigger enforces, so what the tree and the
// Manage Overlays counts report is what actually renders. Empty string =
// that slot shows nothing. Caller holds trigStoreMu (ov comes from
// trigOverrides).
func effAlertTexts(t *GinaTrigger, ov *TriggerOverride) (match, ending, ended string) {
	if bool(t.UseText) {
		match = strings.TrimSpace(t.DisplayText)
	}
	if ov != nil && ov.Text != nil && match != "" {
		match = strings.TrimSpace(*ov.Text)
	}
	isTimer := t.TimerType == "Timer"
	if isTimer && bool(t.UseTimerEnding) && t.TimerEndingTime > 0 && t.TimerEndingTrigger != nil &&
		bool(t.TimerEndingTrigger.UseText) {
		ending = strings.TrimSpace(t.TimerEndingTrigger.DisplayText)
	}
	if ov != nil && ov.EndingText != nil && ending != "" {
		ending = strings.TrimSpace(*ov.EndingText)
	}
	if isTimer && bool(t.UseTimerEnded) && t.TimerEndedTrigger != nil &&
		bool(t.TimerEndedTrigger.UseText) {
		ended = strings.TrimSpace(t.TimerEndedTrigger.DisplayText)
	}
	if ov != nil && ov.EndedText != nil && ended != "" {
		ended = strings.TrimSpace(*ov.EndedText)
	}
	return
}

// effCategoryLocked is a trigger's EFFECTIVE overlay assignment: the local
// Overlay override when present, else the package Category. Caller holds
// trigStoreMu.
func effCategoryLocked(g *GinaGroup, t *GinaTrigger) string {
	if g != nil {
		if ov := trigOverrides[trigToggleKey(g, t)]; ov != nil && ov.Overlay != nil {
			return strings.TrimSpace(*ov.Overlay)
		}
	}
	return strings.TrimSpace(t.Category)
}

// migrateTrigKeyLocked moves every per-trigger local record — enable toggles
// (all characters + seed), audio mutes, clipboard blocks, and overrides —
// from oldKey to newKey, persisting each store that changed. A trigger
// rename changes its stable key; before this helper only the toggles were
// migrated (SaveTrigger's inline block), silently orphaning a member's mute
// and clipboard block. Caller holds trigStoreMu.
func migrateTrigKeyLocked(oldKey, newKey string) {
	if oldKey == newKey {
		return
	}
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
	if v, ok := trigMuteTriggers[oldKey]; ok {
		delete(trigMuteTriggers, oldKey)
		trigMuteTriggers[newKey] = v
		_ = saveTrigMutesLocked()
	}
	if v, ok := trigClipTriggers[oldKey]; ok {
		delete(trigClipTriggers, oldKey)
		trigClipTriggers[newKey] = v
		_ = saveTrigClipsLocked()
	}
	if ov, ok := trigOverrides[oldKey]; ok {
		delete(trigOverrides, oldKey)
		trigOverrides[newKey] = ov
		_ = saveTrigOverridesLocked()
	}
}

// removeTrigKeyLocked deletes every per-trigger store's record for a key —
// used by the explicit delete paths so a future trigger reusing the name
// doesn't inherit stale state. (A package republish deliberately does NOT
// sweep: an override whose trigger vanished comes back if the trigger does.)
// Caller holds trigStoreMu and persists via saveTrigKeyStoresLocked after
// the batch.
func removeTrigKeyLocked(key string) {
	forEachTrigToggleSetLocked(func(s *trigToggleSet) { delete(s.Triggers, key) })
	delete(trigMuteTriggers, key)
	delete(trigClipTriggers, key)
	delete(trigOverrides, key)
}

// saveTrigKeyStoresLocked persists every per-key store after a bulk change
// (deletes touch several stores at once; the files are tiny). Caller holds
// trigStoreMu.
func saveTrigKeyStoresLocked() {
	_ = saveTrigTogglesLocked()
	_ = saveTrigMutesLocked()
	_ = saveTrigClipsLocked()
	_ = saveTrigOverridesLocked()
}

// trigColorRE: the per-trigger color is rendered into inline CSS, so only a
// plain hex value is accepted.
var trigColorRE = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// normalizeOverride folds fields that merely restate the package back to nil
// — a redundant override must never shadow a future officer republish — and
// enforces the reword-only and slot-armed rules. Returns nil when nothing
// survives. Caller holds trigStoreMu (reads the trigger).
func normalizeOverride(t *GinaTrigger, in TriggerOverride) *TriggerOverride {
	out := in

	// normText: canAdd=false is the reword-only rule (alert text); TTS may add.
	normText := func(p *string, pkg string, canAdd bool) *string {
		if p == nil {
			return nil
		}
		v := strings.TrimSpace(*p)
		if !canAdd && pkg == "" {
			return nil // no package text to reword or suppress
		}
		if v == pkg {
			return nil // restates the package
		}
		return &v
	}
	normSound := func(p *string, pkg string) *string {
		if p == nil {
			return nil
		}
		v := ""
		if strings.TrimSpace(*p) != "" {
			v = mediaBasename(*p)
		}
		if v == pkg {
			return nil
		}
		return &v
	}

	// Package channel values, resolved the way the engine resolves them.
	pkgText := ""
	if bool(t.UseText) {
		pkgText = strings.TrimSpace(t.DisplayText)
	}
	pkgTTS := ""
	if bool(t.UseTextToVoice) {
		pkgTTS = strings.TrimSpace(t.TextToVoiceText)
		if pkgTTS == "" {
			pkgTTS = strings.TrimSpace(t.DisplayText)
		}
	}
	pkgSound := ""
	if bool(t.PlayMediaFile) && strings.TrimSpace(t.MediaFileName) != "" {
		pkgSound = mediaBasename(t.MediaFileName)
	}

	// normBool folds a flag override that restates the package back to nil.
	normBool := func(p *bool, pkg bool) *bool {
		if p == nil || *p == pkg {
			return nil
		}
		v := *p
		return &v
	}

	out.Text = normText(out.Text, pkgText, false)
	out.TTS = normText(out.TTS, pkgTTS, true)
	out.Sound = normSound(out.Sound, pkgSound)
	// Fold against the raw package flag — the engine applies it to whatever
	// speech is effective, member-added speech included.
	out.Interrupt = normBool(out.Interrupt, bool(t.InterruptSpeech))

	isTimer := t.TimerType == "Timer"

	// "If already running": only the engine's three known values, only for a
	// timer, and folded against the package behavior (with GINA's retired
	// RestartBasedOnTimerName flag collapsed the way the engine collapses it).
	if out.StartBehavior != nil {
		v := strings.TrimSpace(*out.StartBehavior)
		pkgBehavior := t.TimerStartBehavior
		if bool(t.RestartBasedOnTimerName) {
			pkgBehavior = "RestartTimer"
		}
		valid := v == "StartNewTimer" || v == "RestartTimer" || v == "IgnoreIfRunning"
		if !isTimer || !valid || v == pkgBehavior {
			out.StartBehavior = nil
		} else {
			out.StartBehavior = &v
		}
	}

	if isTimer && bool(t.UseTimerEnding) && t.TimerEndingTime > 0 {
		pkg := endActionsFrom(t.TimerEndingTrigger)
		out.EndingText = normText(out.EndingText, strings.TrimSpace(pkg.text), false)
		out.EndingTTS = normText(out.EndingTTS, strings.TrimSpace(pkg.ttsText), true)
		out.EndingSound = normSound(out.EndingSound, pkg.media)
		out.EndingInterrupt = normBool(out.EndingInterrupt, pkg.ttsInterrupt)
		if out.EndingSeconds != nil && (*out.EndingSeconds <= 0 || *out.EndingSeconds == t.TimerEndingTime) {
			out.EndingSeconds = nil
		}
	} else {
		out.EndingText, out.EndingTTS, out.EndingSound = nil, nil, nil
		out.EndingInterrupt, out.EndingSeconds = nil, nil
	}
	if isTimer && bool(t.UseTimerEnded) {
		pkg := endActionsFrom(t.TimerEndedTrigger)
		out.EndedText = normText(out.EndedText, strings.TrimSpace(pkg.text), false)
		out.EndedTTS = normText(out.EndedTTS, strings.TrimSpace(pkg.ttsText), true)
		out.EndedSound = normSound(out.EndedSound, pkg.media)
		out.EndedInterrupt = normBool(out.EndedInterrupt, pkg.ttsInterrupt)
	} else {
		out.EndedText, out.EndedTTS, out.EndedSound = nil, nil, nil
		out.EndedInterrupt = nil
	}

	// Timer name: empty means "follow the package" rather than "no name" (a
	// bar always displays something — the engine falls back to the trigger
	// name), so unlike the channels above there is no explicit-off state.
	if out.TimerName != nil {
		v := strings.TrimSpace(*out.TimerName)
		if !isTimer || v == "" || v == strings.TrimSpace(t.TimerName) {
			out.TimerName = nil
		} else {
			out.TimerName = &v
		}
	}

	if out.Overlay != nil {
		v := strings.TrimSpace(*out.Overlay)
		if strings.EqualFold(v, strings.TrimSpace(t.Category)) {
			out.Overlay = nil // the package assignment
		} else {
			out.Overlay = &v
		}
	}
	if out.Color != nil {
		v := strings.TrimSpace(*out.Color)
		if v == "" {
			out.Color = nil // the overlay's own color
		} else {
			out.Color = &v
		}
	}

	if out.empty() {
		return nil
	}
	return &out
}

// ── bindings ────────────────────────────────────────────────────────────────

// SetTriggerOverride replaces one trigger's customization wholesale, keyed by
// its group + name (triggers have no persistent id of their own). Available
// to everyone — members and officers alike — and never touches the package
// XML: normalization folds every field equal to the package value back to
// nil, and an override left fully empty deletes the entry.
func (a *App) SetTriggerOverride(groupID int, name string, in TriggerOverride) error {
	if in.Color != nil {
		if c := strings.TrimSpace(*in.Color); c != "" && !trigColorRE.MatchString(c) {
			return fmt.Errorf("color must be a #rrggbb hex value")
		}
	}
	key := strconv.Itoa(groupID) + "/" + name
	trigStoreMu.Lock()
	var t *GinaTrigger
	if g := groupByID[groupID]; g != nil {
		for _, cand := range g.Triggers {
			if cand.Name == name {
				t = cand
				break
			}
		}
	}
	if t == nil {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
	}
	if ov := normalizeOverride(t, in); ov == nil {
		delete(trigOverrides, key)
	} else {
		trigOverrides[key] = ov
	}
	err := saveTrigOverridesLocked()
	trigStoreMu.Unlock()
	// Recompile so the customization takes effect immediately (it's baked into
	// each compiled trigger, like the mutes).
	RebuildTriggerActivation()
	emitTriggersChanged()
	if err == nil {
		auditEventHourly("app_updatedtimer_customized", "")
	}
	return err
}

// ReassignTriggersOverlay moves specific triggers (session ids from the tree)
// to another overlay ("" = unassigned) — the bulk re-assign action. Authority
// follows the edit rules: Personal triggers are edited directly; Fuse
// triggers get a package edit (publishable, as usual) when the caller is an
// officer, and a local Overlay override otherwise.
func (a *App) ReassignTriggersOverlay(triggerIDs []int, overlay string) error {
	overlay = strings.TrimSpace(overlay)
	trigStoreMu.Lock()
	officer := isOfficerCached()
	fuseTouched, edited, ovChanged := false, false, false
	for _, id := range triggerIDs {
		t := trigByID[id]
		g := trigGroupOf[id]
		if t == nil || g == nil {
			continue
		}
		key := trigToggleKey(g, t)
		if isFuseGroupLocked(g.GroupID) && !officer {
			// Member: a local override, never a package edit. Equal to the
			// package assignment folds back to "no override".
			ov := trigOverrides[key]
			if strings.EqualFold(strings.TrimSpace(t.Category), overlay) {
				if ov != nil && ov.Overlay != nil {
					ov.Overlay = nil
					if ov.empty() {
						delete(trigOverrides, key)
					}
					ovChanged = true
				}
			} else {
				if ov == nil {
					ov = &TriggerOverride{}
					trigOverrides[key] = ov
				}
				v := overlay
				ov.Overlay = &v
				ovChanged = true
			}
			continue
		}
		// Direct edit (Personal, or Fuse as officer). A local override pointing
		// elsewhere would shadow the rewrite, so it folds into the new value.
		if ov := trigOverrides[key]; ov != nil && ov.Overlay != nil {
			ov.Overlay = nil
			if ov.empty() {
				delete(trigOverrides, key)
			}
			ovChanged = true
		}
		if t.Category != overlay {
			t.Category = overlay
			edited = true
			if isFuseGroupLocked(g.GroupID) {
				fuseTouched = true
			}
		}
	}
	var err error
	if edited {
		err = saveTriggersLocked()
	}
	if ovChanged {
		if e := saveTrigOverridesLocked(); err == nil {
			err = e
		}
	}
	trigStoreMu.Unlock()
	if err != nil {
		return err
	}
	if fuseTouched {
		markFuseDirty()
	}
	RebuildTriggerActivation()
	emitTriggersChanged()
	auditEventHourly("app_updatedtimer_reassignedoverlay", "")
	return nil
}

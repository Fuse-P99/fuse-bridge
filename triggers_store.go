package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// GINA trigger config storage. The app keeps its OWN copy of the GINA-format
// XML (imported once from the user's GINAConfig.xml) at triggersPath() and
// never writes GINA's original file — GINA rewrites that file itself, so two
// writers would clobber each other. Re-importing overwrites the local copy.
//
// The Trigger/TriggerGroup structs model the full GINA field set so edits
// round-trip losslessly; the Settings/BehaviorGroups/Categories/Characters
// sections are preserved verbatim as raw inner XML (Characters is additionally
// parsed read-only for the per-character enabled-group sets).

// ginaBool marshals as "True"/"False" to match GINA's .NET XmlSerializer
// output (Go's ParseBool already accepts those on read).
type ginaBool bool

func (b ginaBool) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	s := "False"
	if b {
		s = "True"
	}
	return e.EncodeElement(s, start)
}

func (b *ginaBool) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	*b = ginaBool(strings.EqualFold(strings.TrimSpace(s), "true"))
	return nil
}

// GinaEndTrigger is the nested TimerEndingTrigger/TimerEndedTrigger payload
// (what to do when a timer is about to end / has ended).
type GinaEndTrigger struct {
	UseText         ginaBool `xml:"UseText"`
	DisplayText     string   `xml:"DisplayText"`
	UseTextToVoice  ginaBool `xml:"UseTextToVoice"`
	InterruptSpeech ginaBool `xml:"InterruptSpeech"`
	TextToVoiceText string   `xml:"TextToVoiceText"`
	PlayMediaFile   ginaBool `xml:"PlayMediaFile"`
	MediaFileName   string   `xml:"MediaFileName,omitempty"`
}

type GinaEarlyEnder struct {
	EarlyEndText string   `xml:"EarlyEndText"`
	EnableRegex  ginaBool `xml:"EnableRegex"`
}

type GinaEarlyEnders struct {
	Enders []GinaEarlyEnder `xml:"EarlyEnder"`
}

// GinaTrigger models every Trigger field GINA writes (verified against the
// user's config) so unedited fields survive a save. Only a subset drives the
// engine; the rest ride along for future iterations.
type GinaTrigger struct {
	ID                       int              `xml:"-"` // session id for UI addressing (not persisted)
	Name                     string           `xml:"Name"`
	TriggerText              string           `xml:"TriggerText"`
	Comments                 string           `xml:"Comments"`
	EnableRegex              ginaBool         `xml:"EnableRegex"`
	UseText                  ginaBool         `xml:"UseText"`
	DisplayText              string           `xml:"DisplayText"`
	CopyToClipboard          ginaBool         `xml:"CopyToClipboard"`
	ClipboardText            string           `xml:"ClipboardText"`
	UseTextToVoice           ginaBool         `xml:"UseTextToVoice"`
	InterruptSpeech          ginaBool         `xml:"InterruptSpeech"`
	TextToVoiceText          string           `xml:"TextToVoiceText"`
	PlayMediaFile            ginaBool         `xml:"PlayMediaFile"`
	MediaFileName            string           `xml:"MediaFileName,omitempty"`
	TimerType                string           `xml:"TimerType"`
	TimerName                string           `xml:"TimerName"`
	RestartBasedOnTimerName  ginaBool         `xml:"RestartBasedOnTimerName"`
	TimerMillisecondDuration int64            `xml:"TimerMillisecondDuration"`
	TimerDuration            int              `xml:"TimerDuration"`
	TimerVisibleDuration     int              `xml:"TimerVisibleDuration"`
	TimerStartBehavior       string           `xml:"TimerStartBehavior"`
	TimerEndingTime          int              `xml:"TimerEndingTime"`
	UseTimerEnding           ginaBool         `xml:"UseTimerEnding"`
	UseTimerEnded            ginaBool         `xml:"UseTimerEnded"`
	TimerEndingTrigger       *GinaEndTrigger  `xml:"TimerEndingTrigger,omitempty"`
	TimerEndedTrigger        *GinaEndTrigger  `xml:"TimerEndedTrigger,omitempty"`
	UseCounterResetTimer     ginaBool         `xml:"UseCounterResetTimer"`
	CounterResetDuration     int              `xml:"CounterResetDuration"`
	Category                 string           `xml:"Category"`
	Modified                 string           `xml:"Modified"`
	UseFastCheck             ginaBool         `xml:"UseFastCheck"`
	TimerEarlyEnders         *GinaEarlyEnders `xml:"TimerEarlyEnders,omitempty"`
}

type GinaGroup struct {
	Name            string         `xml:"Name"`
	Comments        string         `xml:"Comments"`
	SelfCommented   ginaBool       `xml:"SelfCommented"`
	GroupID         int            `xml:"GroupId"`
	EnableByDefault ginaBool       `xml:"EnableByDefault"`
	Groups          []*GinaGroup   `xml:"TriggerGroups>TriggerGroup,omitempty"`
	Triggers        []*GinaTrigger `xml:"Triggers>Trigger,omitempty"`
}

// rawXML preserves a section verbatim across the unmarshal→marshal round trip.
type rawXML struct {
	Inner []byte `xml:",innerxml"`
}

type ginaConfig struct {
	XMLName        xml.Name     `xml:"Configuration"`
	Settings       rawXML       `xml:"Settings"`
	BehaviorGroups rawXML       `xml:"BehaviorGroups"`
	Categories     rawXML       `xml:"Categories"`
	Groups         []*GinaGroup `xml:"TriggerGroups>TriggerGroup"`
	Characters     rawXML       `xml:"Characters"`
}

// ginaCharacter is the read-only view of a <Character> entry: which trigger
// groups that character has enabled (flat, explicit GroupId list).
type ginaCharacter struct {
	Name        string `xml:"Name"`
	LogFilePath string `xml:"LogFilePath"`
	Groups      []struct {
		GroupID string `xml:"GroupId,attr"`
	} `xml:"TriggerGroups>TriggerGroup"`
}

var (
	trigStoreMu    sync.Mutex
	trigCfg        *ginaConfig             // nil until a copy is imported+loaded
	trigByID       map[int]*GinaTrigger    // session trigger id → trigger
	trigGroupOf    map[int]*GinaGroup      // session trigger id → containing group
	groupByID      map[int]*GinaGroup      // GroupId → group
	groupParentOf  map[int]*GinaGroup      // GroupId → parent group (nil for roots)
	trigNextID     int                     // next session trigger id
	trigCharGroups map[string]map[int]bool // lower(char name) → enabled GroupId set
	// User on/off toggles from the edit tree, layered over GINA's per-character
	// enablement. Groups key by GroupId; triggers by "GroupId/Name" (triggers
	// have no persistent id of their own). Persisted in trigger_toggles.json.
	trigGroupToggle map[int]bool
	trigTrigToggle  map[string]bool
)

func triggersPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "triggers.xml")
}

// ginaDefaultPath is where GINA keeps its config on this machine.
func ginaDefaultPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "GimaSoft", "GINA", "GINAConfig.xml")
}

// LoadTriggers parses the app's trigger copy (if present) and rebuilds the
// engine's active set. Missing file just means "not imported yet".
func LoadTriggers() {
	data, err := os.ReadFile(triggersPath())
	if err != nil {
		return
	}
	var cfg ginaConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		addStatus("Triggers: couldn't parse triggers.xml: %v", err)
		return
	}
	trigStoreMu.Lock()
	trigCfg = &cfg
	rebuildTriggerIndexLocked()
	trigCharGroups = parseCharGroups(cfg.Characters.Inner)
	loadTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
}

// ── user enable/disable toggles (edit-tree sliders) ─────────────────────────

type trigTogglesFile struct {
	Groups   map[string]bool `json:"groups"`
	Triggers map[string]bool `json:"triggers"`
}

func trigTogglesPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "trigger_toggles.json")
}

// trigTimersStatePath holds the persisted Buffs (Self)/Disciplines timers that
// survive app restarts (see persistTriggerTimers in triggers_engine.go).
func trigTimersStatePath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "trigger_timers.json")
}

func trigToggleKey(g *GinaGroup, t *GinaTrigger) string {
	return strconv.Itoa(g.GroupID) + "/" + t.Name
}

func loadTrigTogglesLocked() {
	trigGroupToggle = make(map[int]bool)
	trigTrigToggle = make(map[string]bool)
	data, err := os.ReadFile(trigTogglesPath())
	if err != nil {
		return
	}
	var f trigTogglesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	for k, v := range f.Groups {
		if id, err := strconv.Atoi(k); err == nil {
			trigGroupToggle[id] = v
		}
	}
	for k, v := range f.Triggers {
		trigTrigToggle[k] = v
	}
}

func saveTrigTogglesLocked() error {
	f := trigTogglesFile{
		Groups:   make(map[string]bool, len(trigGroupToggle)),
		Triggers: trigTrigToggle,
	}
	for id, v := range trigGroupToggle {
		f.Groups[strconv.Itoa(id)] = v
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(trigTogglesPath(), data, 0600)
}

// effectiveGroupEnabledLocked layers the user's slider over GINA's
// per-character enablement (enabledSet nil = character unknown → all enabled;
// in-app groups above trigLocalGroupIDBase default to enabled).
func effectiveGroupEnabledLocked(g *GinaGroup, enabledSet map[int]bool) bool {
	if v, ok := trigGroupToggle[g.GroupID]; ok {
		return v
	}
	return enabledSet == nil || enabledSet[g.GroupID] || g.GroupID > trigLocalGroupIDBase
}

// effectiveTriggerEnabledLocked: triggers default to on; the slider overrides.
func effectiveTriggerEnabledLocked(g *GinaGroup, t *GinaTrigger) bool {
	if v, ok := trigTrigToggle[trigToggleKey(g, t)]; ok {
		return v
	}
	return true
}

// ImportGINAConfig validates the GINA file at path and installs it as the
// app's copy (overwriting any local edits), then loads it. Returns counts.
func ImportGINAConfig(path string) (groups, triggers int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("couldn't read %s: %w", path, err)
	}
	var cfg ginaConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return 0, 0, fmt.Errorf("not a valid GINA config: %w", err)
	}
	dst := triggersPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return 0, 0, err
	}
	// Install the ORIGINAL bytes (full fidelity), atomically.
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return 0, 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return 0, 0, err
	}
	LoadTriggers()
	g, t := countGinaTree(cfg.Groups)
	addStatus("Triggers: imported %d triggers in %d groups from GINA", t, g)
	return g, t, nil
}

func countGinaTree(groups []*GinaGroup) (g, t int) {
	for _, gr := range groups {
		g++
		t += len(gr.Triggers)
		cg, ct := countGinaTree(gr.Groups)
		g += cg
		t += ct
	}
	return
}

// rebuildTriggerIndexLocked assigns session IDs to any trigger that lacks one
// and rebuilds the lookup maps. Existing IDs are preserved so the UI (and the
// activity feed) keeps addressing the same triggers across edits.
func rebuildTriggerIndexLocked() {
	trigByID = make(map[int]*GinaTrigger)
	trigGroupOf = make(map[int]*GinaGroup)
	groupByID = make(map[int]*GinaGroup)
	groupParentOf = make(map[int]*GinaGroup)
	var walk func(g *GinaGroup, parent *GinaGroup)
	walk = func(g *GinaGroup, parent *GinaGroup) {
		groupByID[g.GroupID] = g
		groupParentOf[g.GroupID] = parent
		for _, t := range g.Triggers {
			if t.ID == 0 {
				trigNextID++
				t.ID = trigNextID
			}
			trigByID[t.ID] = t
			trigGroupOf[t.ID] = g
		}
		for _, c := range g.Groups {
			walk(c, g)
		}
	}
	if trigCfg != nil {
		for _, g := range trigCfg.Groups {
			walk(g, nil)
		}
	}
}

// parseCharGroups extracts each character's enabled GroupId set from the raw
// <Characters> section. Keys: lowercased character name AND the toon name
// derived from the character's log file path (either can match the tailed toon).
func parseCharGroups(raw []byte) map[string]map[int]bool {
	out := make(map[string]map[int]bool)
	if len(raw) == 0 {
		return out
	}
	var chars struct {
		Characters []ginaCharacter `xml:"Character"`
	}
	wrapped := append(append([]byte("<Characters>"), raw...), []byte("</Characters>")...)
	if err := xml.Unmarshal(wrapped, &chars); err != nil {
		return out
	}
	for _, c := range chars.Characters {
		set := make(map[int]bool, len(c.Groups))
		for _, g := range c.Groups {
			if id, err := strconv.Atoi(strings.TrimSpace(g.GroupID)); err == nil {
				set[id] = true
			}
		}
		if n := strings.ToLower(strings.TrimSpace(c.Name)); n != "" {
			out[n] = set
		}
		if c.LogFilePath != "" {
			if n := strings.ToLower(charNameFromLog(filepath.Base(c.LogFilePath))); n != "" {
				out[n] = set
			}
		}
	}
	return out
}

// enabledGroupsForLocked returns the enabled GroupId set for a character, or
// nil meaning "all groups enabled" (character unknown → run everything).
func enabledGroupsForLocked(charName string) map[int]bool {
	if set, ok := trigCharGroups[strings.ToLower(strings.TrimSpace(charName))]; ok {
		return set
	}
	return nil
}

// saveTriggersLocked marshals the config back to triggers.xml atomically.
func saveTriggersLocked() error {
	if trigCfg == nil {
		return fmt.Errorf("no triggers loaded")
	}
	body, err := xml.MarshalIndent(trigCfg, "", "  ")
	if err != nil {
		return err
	}
	data := append([]byte(xml.Header), body...)
	dst := triggersPath()
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// nextGroupIDLocked returns an unused GroupId for a newly created group.
func nextGroupIDLocked() int {
	max := 0
	for id := range groupByID {
		if id > max {
			max = id
		}
	}
	if max < 1000000 {
		max = 1000000 // keep our IDs clear of GINA's existing ranges
	}
	return max + 1
}

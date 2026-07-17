package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Wails bindings for the Triggers tab: live state (alert + timers + activity)
// polled by the frontend, plus the edit view's tree and mutation calls.

type TriggerAlertUI struct {
	Text string `json:"text"`
	AtMs int64  `json:"at_ms"`
}

type TriggerTimerUI struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	StartedAtMs int64  `json:"started_at_ms"`
	EndsAtMs    int64  `json:"ends_at_ms"`
	TriggerID   int    `json:"trigger_id"`
}

type TriggerActivityUI struct {
	AtMs      int64    `json:"at_ms"`
	Path      []string `json:"path"`
	TriggerID int      `json:"trigger_id"`
}

type TriggerStateUI struct {
	Imported  bool                `json:"imported"`
	Character string              `json:"character"`
	Alert     *TriggerAlertUI     `json:"alert"`
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
	if trigAlertCur != nil {
		out.Alert = &TriggerAlertUI{Text: trigAlertCur.text, AtMs: trigAlertCur.at.UnixMilli()}
	}
	for _, lt := range liveTimers {
		out.Timers = append(out.Timers, TriggerTimerUI{
			ID: lt.id, Name: lt.name, Category: lt.category,
			StartedAtMs: lt.startedAt.UnixMilli(), EndsAtMs: lt.endsAt.UnixMilli(),
			TriggerID: lt.triggerID,
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

// ImportGINATriggers imports (or re-imports) the GINA config into the app's
// own copy. Uses GINA's default location; falls back to a file picker.
func (a *App) ImportGINATriggers() (string, error) {
	path := ginaDefaultPath()
	if _, err := os.Stat(path); err != nil {
		if a.ctx == nil {
			return "", fmt.Errorf("GINA config not found at %s", path)
		}
		picked, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
			Title: "Select your GINAConfig.xml",
			Filters: []wailsruntime.FileFilter{
				{DisplayName: "GINA config (*.xml)", Pattern: "*.xml"},
			},
		})
		if err != nil || picked == "" {
			return "", fmt.Errorf("no file selected")
		}
		path = picked
	}
	groups, triggers, err := ImportGINAConfig(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Imported %d triggers in %d groups", triggers, groups), nil
}

// ── edit view ────────────────────────────────────────────────────────────────

// TriggerEditUI is a trigger reduced to the fields the app currently uses;
// it doubles as the payload for SaveTrigger/CreateTrigger.
type TriggerEditUI struct {
	ID                      int    `json:"id"`
	GroupID                 int    `json:"group_id"`
	Name                    string `json:"name"`
	TriggerText             string `json:"trigger_text"`
	EnableRegex             bool   `json:"enable_regex"`
	UseText                 bool   `json:"use_text"`
	DisplayText             string `json:"display_text"`
	TimerEnabled            bool   `json:"timer_enabled"`
	TimerName               string `json:"timer_name"`
	TimerSeconds            int    `json:"timer_seconds"`
	RestartBasedOnTimerName bool   `json:"restart_based_on_timer_name"`
	TimerStartBehavior      string `json:"timer_start_behavior"`
	UseTimerEnded           bool   `json:"use_timer_ended"`
	TimerEndedText          string `json:"timer_ended_text"`
	Category                string `json:"category"`
	Unsupported             bool   `json:"unsupported"`
	Enabled                 bool   `json:"enabled"`
}

type TriggerGroupUI struct {
	ID       int              `json:"id"`
	Name     string           `json:"name"`
	Enabled  bool             `json:"enabled"`
	Groups   []TriggerGroupUI `json:"groups"`
	Triggers []TriggerEditUI  `json:"triggers"`
}

func triggerToUI(t *GinaTrigger, g *GinaGroup) TriggerEditUI {
	durMs := t.TimerMillisecondDuration
	if durMs <= 0 {
		durMs = int64(t.TimerDuration) * 1000
	}
	endedText := ""
	if t.TimerEndedTrigger != nil {
		endedText = t.TimerEndedTrigger.DisplayText
	}
	return TriggerEditUI{
		ID:                      t.ID,
		GroupID:                 g.GroupID,
		Name:                    t.Name,
		TriggerText:             t.TriggerText,
		EnableRegex:             bool(t.EnableRegex),
		UseText:                 bool(t.UseText),
		DisplayText:             t.DisplayText,
		TimerEnabled:            t.TimerType == "Timer",
		TimerName:               t.TimerName,
		TimerSeconds:            int(durMs / 1000),
		RestartBasedOnTimerName: bool(t.RestartBasedOnTimerName),
		TimerStartBehavior:      t.TimerStartBehavior,
		UseTimerEnded:           bool(t.UseTimerEnded),
		TimerEndedText:          endedText,
		Category:                t.Category,
		Unsupported:             !patternSupported(t.ID, t.TriggerText, bool(t.EnableRegex)),
		Enabled:                 effectiveTriggerEnabledLocked(g, t),
	}
}

// GetTriggerTree returns the full group/trigger hierarchy for the edit view.
// Enabled reflects the current character's GINA enablement.
func (a *App) GetTriggerTree() []TriggerGroupUI {
	trigStoreMu.Lock()
	defer trigStoreMu.Unlock()
	if trigCfg == nil {
		return []TriggerGroupUI{}
	}
	enabled := enabledGroupsForLocked(currentCharName)
	var conv func(g *GinaGroup) TriggerGroupUI
	conv = func(g *GinaGroup) TriggerGroupUI {
		ug := TriggerGroupUI{
			ID:       g.GroupID,
			Name:     g.Name,
			Enabled:  effectiveGroupEnabledLocked(g, enabled),
			Groups:   make([]TriggerGroupUI, 0, len(g.Groups)),
			Triggers: make([]TriggerEditUI, 0, len(g.Triggers)),
		}
		for _, t := range g.Triggers {
			ug.Triggers = append(ug.Triggers, triggerToUI(t, g))
		}
		for _, c := range g.Groups {
			ug.Groups = append(ug.Groups, conv(c))
		}
		return ug
	}
	out := make([]TriggerGroupUI, 0, len(trigCfg.Groups))
	for _, g := range trigCfg.Groups {
		out = append(out, conv(g))
	}
	return out
}

// applyTriggerEdit writes the editable fields onto a GinaTrigger.
func applyTriggerEdit(t *GinaTrigger, in TriggerEditUI) {
	t.Name = strings.TrimSpace(in.Name)
	t.TriggerText = in.TriggerText
	t.EnableRegex = ginaBool(in.EnableRegex)
	t.UseText = ginaBool(in.UseText)
	t.DisplayText = in.DisplayText
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
	t.RestartBasedOnTimerName = ginaBool(in.RestartBasedOnTimerName)
	if in.TimerStartBehavior != "" {
		t.TimerStartBehavior = in.TimerStartBehavior
	}
	t.UseTimerEnded = ginaBool(in.UseTimerEnded)
	if in.UseTimerEnded {
		if t.TimerEndedTrigger == nil {
			t.TimerEndedTrigger = &GinaEndTrigger{}
		}
		t.TimerEndedTrigger.UseText = true
		t.TimerEndedTrigger.DisplayText = in.TimerEndedText
	}
	t.Category = strings.TrimSpace(in.Category)
	t.Modified = time.Now().Format("2006-01-02T15:04:05")
}

// SaveTrigger updates an existing trigger and persists + reactivates.
func (a *App) SaveTrigger(in TriggerEditUI) error {
	trigStoreMu.Lock()
	t, ok := trigByID[in.ID]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
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
		if v, ok := trigTrigToggle[oldKey]; ok {
			delete(trigTrigToggle, oldKey)
			trigTrigToggle[newKey] = v
			_ = saveTrigTogglesLocked()
		}
	}
	err := saveTriggersLocked()
	trigStoreMu.Unlock()
	invalidateTrigSupport(in.ID)
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
	trigNextID++
	t.ID = trigNextID
	g.Triggers = append(g.Triggers, t)
	trigByID[t.ID] = t
	trigGroupOf[t.ID] = g
	err := saveTriggersLocked()
	id := t.ID
	trigStoreMu.Unlock()
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
	for i, t := range g.Triggers {
		if t.ID == id {
			g.Triggers = append(g.Triggers[:i], g.Triggers[i+1:]...)
			break
		}
	}
	delete(trigByID, id)
	delete(trigGroupOf, id)
	err := saveTriggersLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

// CreateTriggerGroup adds a group under parentID (0 = top level).
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
	g := &GinaGroup{Name: name, GroupID: nextGroupIDLocked()}
	if parentID == 0 {
		trigCfg.Groups = append(trigCfg.Groups, g)
		groupParentOf[g.GroupID] = nil
	} else {
		p, ok := groupByID[parentID]
		if !ok {
			trigStoreMu.Unlock()
			return 0, fmt.Errorf("parent group not found")
		}
		p.Groups = append(p.Groups, g)
		groupParentOf[g.GroupID] = p
	}
	groupByID[g.GroupID] = g
	err := saveTriggersLocked()
	id := g.GroupID
	trigStoreMu.Unlock()
	return id, err
}

// SetTriggerGroupEnabled flips a group's on/off slider (stored as a local
// override on top of GINA's per-character enablement) and reactivates.
func (a *App) SetTriggerGroupEnabled(id int, enabled bool) error {
	trigStoreMu.Lock()
	if _, ok := groupByID[id]; !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("group not found")
	}
	trigGroupToggle[id] = enabled
	err := saveTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

// SetTriggerEnabled flips a single trigger's on/off slider and reactivates.
func (a *App) SetTriggerEnabled(id int, enabled bool) error {
	trigStoreMu.Lock()
	t, ok := trigByID[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("trigger not found")
	}
	trigTrigToggle[trigToggleKey(trigGroupOf[id], t)] = enabled
	err := saveTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

// RenameTriggerGroup renames a group and persists.
func (a *App) RenameTriggerGroup(id int, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	trigStoreMu.Lock()
	defer trigStoreMu.Unlock()
	g, ok := groupByID[id]
	if !ok {
		return fmt.Errorf("group not found")
	}
	g.Name = name
	return saveTriggersLocked()
}

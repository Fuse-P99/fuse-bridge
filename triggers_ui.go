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

// SyncTriggers downloads the guild's Fuse Triggers from the server (and seeds it
// if we're the first officer). Called by the frontend when the Timers window
// opens so users always have the latest shared set. Runs in the background.
func (a *App) SyncTriggers() { go SyncFuseTriggers() }

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
	// Editable is false for Fuse Triggers content when the user isn't an officer
	// (they can still enable/disable, just not change the trigger).
	Editable bool `json:"editable"`
}

type TriggerGroupUI struct {
	ID       int              `json:"id"`
	Name     string           `json:"name"`
	Enabled  bool             `json:"enabled"`
	Groups   []TriggerGroupUI `json:"groups"`
	Triggers []TriggerEditUI  `json:"triggers"`
	// TotalTriggers counts the whole subtree (this group + all descendants).
	TotalTriggers int `json:"total_triggers"`
	// Editable: officers can edit Fuse groups; everyone can edit Personal.
	Editable bool `json:"editable"`
	// Personal marks the user-owned subtree (vs the server-synced Fuse set).
	Personal bool `json:"personal"`
}

func triggerToUI(t *GinaTrigger, g *GinaGroup, editable bool) TriggerEditUI {
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
		Editable:                editable,
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
	officer := isOfficerCached()
	var conv func(g *GinaGroup) TriggerGroupUI
	conv = func(g *GinaGroup) TriggerGroupUI {
		personal := isPersonalGroupLocked(g.GroupID)
		editable := personal || officer
		ug := TriggerGroupUI{
			ID:       g.GroupID,
			Name:     g.Name,
			Enabled:  effectiveGroupEnabledLocked(g),
			Groups:   make([]TriggerGroupUI, 0, len(g.Groups)),
			Triggers: make([]TriggerEditUI, 0, len(g.Triggers)),
			Editable: editable,
			Personal: personal,
		}
		for _, t := range g.Triggers {
			ug.Triggers = append(ug.Triggers, triggerToUI(t, g, editable))
		}
		ug.TotalTriggers = len(ug.Triggers)
		for _, c := range g.Groups {
			cu := conv(c)
			ug.TotalTriggers += cu.TotalTriggers
			// Hide GINA groups with no triggers anywhere beneath them —
			// vestigial import leftovers (e.g. empty "On"/"Off" shells).
			// App-created groups stay visible so New Group is usable.
			if cu.TotalTriggers == 0 && cu.ID <= trigLocalGroupIDBase {
				continue
			}
			ug.Groups = append(ug.Groups, cu)
		}
		sortTriggerGroups(ug.Groups)
		return ug
	}
	out := make([]TriggerGroupUI, 0, len(trigCfg.Groups))
	for _, g := range trigCfg.Groups {
		gu := conv(g)
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

// checkFuseEditLocked reports whether groupID is in the Fuse subtree and, if so,
// rejects the edit for non-officers. Caller holds trigStoreMu. When fuse is
// true and err is nil, the caller must pushFuseTriggersAsync() after saving.
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
		pushFuseTriggersAsync()
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
	trigNextID++
	t.ID = trigNextID
	g.Triggers = append(g.Triggers, t)
	trigByID[t.ID] = t
	trigGroupOf[t.ID] = g
	err = saveTriggersLocked()
	id := t.ID
	trigStoreMu.Unlock()
	if fuse {
		pushFuseTriggersAsync()
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
		pushFuseTriggersAsync()
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
		pushFuseTriggersAsync()
	}
	return id, err
}

// SetTriggerGroupEnabled flips a group's on/off slider and cascades it through
// the whole subtree: every nested subgroup AND every trigger inside gets the
// same state (stored as local overrides on top of GINA's per-character
// enablement), so the switch visibly turns everything under it on or off.
func (a *App) SetTriggerGroupEnabled(id int, enabled bool) error {
	trigStoreMu.Lock()
	g, ok := groupByID[id]
	if !ok {
		trigStoreMu.Unlock()
		return fmt.Errorf("group not found")
	}
	var walk func(g *GinaGroup)
	walk = func(g *GinaGroup) {
		trigGroupToggle[g.GroupID] = enabled
		for _, t := range g.Triggers {
			trigTrigToggle[trigToggleKey(g, t)] = enabled
		}
		for _, c := range g.Groups {
			walk(c)
		}
	}
	walk(g)
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
		pushFuseTriggersAsync()
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
		pushFuseTriggersAsync()
	}
	RebuildTriggerActivation()
	return err
}

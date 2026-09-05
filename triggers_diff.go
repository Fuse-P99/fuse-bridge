package main

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// "View edits" for officers: what the unpublished local Fuse set would change,
// compared against the copy currently published on the server. The comparison
// is against the LIVE server copy (fetched fresh), not the version the officer
// started editing from — so when another officer published in the meantime,
// the diff shows exactly what pressing Publish would do to the guild's current
// set, including undoing that other release. Read-only: nothing here mutates
// either tree or the dirty flag.

// FuseEditChange is one human-readable field difference on a trigger or group.
type FuseEditChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// FuseEditItem is one trigger or group the publish would add, remove, or change.
type FuseEditItem struct {
	Kind    string           `json:"kind"` // "added" | "removed" | "modified" | "moved"
	Name    string           `json:"name"`
	Path    string           `json:"path"`               // group path below the Fuse root ("" = top level)
	OldPath string           `json:"old_path,omitempty"` // moved triggers: where the published copy has it
	Changes []FuseEditChange `json:"changes,omitempty"`
}

// FusePendingEdits is the full unpublished-changes report for the View-edits
// dialog. BaseVersion is what the local edits started from; ServerVersion is
// what is live right now (greater than BaseVersion when another officer
// published in the meantime).
type FusePendingEdits struct {
	BaseVersion   int            `json:"base_version"`
	ServerVersion int            `json:"server_version"`
	Groups        []FuseEditItem `json:"groups"`
	Triggers      []FuseEditItem `json:"triggers"`
}

// GetFusePendingEdits fetches the published Fuse set and reports how the local
// (unpublished) copy differs from it, field by field.
func (a *App) GetFusePendingEdits() (FusePendingEdits, error) {
	out := FusePendingEdits{Groups: []FuseEditItem{}, Triggers: []FuseEditItem{}}
	if !IsLinked() {
		return out, fmt.Errorf("not linked to a Discord account")
	}
	version, payload, ok := fetchFuseTriggers()
	if !ok {
		return out, fmt.Errorf("could not fetch the published Fuse Triggers from the server")
	}
	var srv *GinaGroup
	if version > 0 && strings.TrimSpace(payload) != "" {
		var g GinaGroup
		if xml.Unmarshal([]byte(payload), &g) != nil {
			return out, fmt.Errorf("the server's Fuse Triggers could not be read")
		}
		g.Name = fuseTriggersName
		g.GroupID = fuseRootGroupID
		// The local set stores bare media file names; normalize the server copy
		// the same way (as adoption would) so paths don't diff against basenames.
		scrubMediaNamesInGroup(&g)
		srv = &g
	}

	trigStoreMu.Lock()
	// Refresh the newest-seen server version so the publish bar's "another
	// officer published in the meantime" warning stays accurate too.
	fuseServerVersion = version
	out.BaseVersion = fuseVersion
	out.ServerVersion = version
	out.Groups, out.Triggers = diffFuseTreesLocked(srv, fuseRoot)
	trigStoreMu.Unlock()
	return out, nil
}

// ── tree walk + matching ─────────────────────────────────────────────────────

type fuseDiffGroup struct {
	g    *GinaGroup
	path string // " › "-joined path below the root, ending in this group's name
}

type fuseDiffTrig struct {
	t       *GinaTrigger
	groupID int
	path    string // containing group's path ("" = directly under the root)
}

// collectFuseDiff indexes a Fuse subtree: groups by GroupId, triggers by the
// stable "GroupId/Name" key (the same identity every per-trigger store uses).
func collectFuseDiff(root *GinaGroup) (map[int]fuseDiffGroup, map[string]fuseDiffTrig) {
	groups := map[int]fuseDiffGroup{}
	trigs := map[string]fuseDiffTrig{}
	if root == nil {
		return groups, trigs
	}
	var walk func(g *GinaGroup, path string)
	walk = func(g *GinaGroup, path string) {
		for _, t := range g.Triggers {
			trigs[trigToggleKey(g, t)] = fuseDiffTrig{t: t, groupID: g.GroupID, path: path}
		}
		for _, c := range g.Groups {
			p := c.Name
			if path != "" {
				p = path + " › " + c.Name
			}
			groups[c.GroupID] = fuseDiffGroup{g: c, path: p}
			walk(c, p)
		}
	}
	walk(root, "")
	return groups, trigs
}

// diffFuseTreesLocked compares the published tree (old) against the local tree
// (new). Groups match by GroupId — a renamed group is a rename, not a
// remove+add of everything inside it. Triggers match by group+name, with two
// recovery passes so common edits read as what they are: a same-group
// remove/add pair sharing search text is a rename; a cross-group pair sharing
// name and search text is a move. Caller holds trigStoreMu.
func diffFuseTreesLocked(old, local *GinaGroup) (groups, triggers []FuseEditItem) {
	groups = []FuseEditItem{}
	triggers = []FuseEditItem{}
	oldG, oldT := collectFuseDiff(old)
	newG, newT := collectFuseDiff(local)

	for id, ni := range newG {
		oi, ok := oldG[id]
		if !ok {
			groups = append(groups, FuseEditItem{Kind: "added", Name: ni.g.Name, Path: ni.path})
			continue
		}
		var ch []FuseEditChange
		if oi.g.Name != ni.g.Name {
			ch = append(ch, FuseEditChange{Field: "Name", Old: oi.g.Name, New: ni.g.Name})
		}
		if strings.TrimSpace(oi.g.Comments) != strings.TrimSpace(ni.g.Comments) {
			ch = append(ch, FuseEditChange{Field: "Comments", Old: fedText(oi.g.Comments), New: fedText(ni.g.Comments)})
		}
		if bool(oi.g.EnableByDefault) != bool(ni.g.EnableByDefault) {
			ch = append(ch, FuseEditChange{Field: "Enabled by default", Old: fedOnOff(bool(oi.g.EnableByDefault)), New: fedOnOff(bool(ni.g.EnableByDefault))})
		}
		if len(ch) > 0 {
			groups = append(groups, FuseEditItem{Kind: "modified", Name: ni.g.Name, Path: ni.path, Changes: ch})
		}
	}
	for id, oi := range oldG {
		if _, ok := newG[id]; !ok {
			groups = append(groups, FuseEditItem{Kind: "removed", Name: oi.g.Name, Path: oi.path})
		}
	}

	var addedKeys, removedKeys []string
	for key, ni := range newT {
		if oi, ok := oldT[key]; ok {
			if ch := diffFuseTrigger(oi.t, ni.t); len(ch) > 0 {
				triggers = append(triggers, FuseEditItem{Kind: "modified", Name: ni.t.Name, Path: ni.path, Changes: ch})
			}
		} else {
			addedKeys = append(addedKeys, key)
		}
	}
	for key := range oldT {
		if _, ok := newT[key]; !ok {
			removedKeys = append(removedKeys, key)
		}
	}
	sort.Strings(addedKeys)
	sort.Strings(removedKeys)

	consumedAdd := map[string]bool{}
	consumedRem := map[string]bool{}
	// Pass 1: renames — same group, same search text, unambiguous pairing.
	for _, rk := range removedKeys {
		oi := oldT[rk]
		match, matches := "", 0
		for _, ak := range addedKeys {
			if consumedAdd[ak] {
				continue
			}
			ni := newT[ak]
			if ni.groupID == oi.groupID && ni.t.TriggerText == oi.t.TriggerText {
				match = ak
				matches++
			}
		}
		if matches != 1 {
			continue
		}
		ni := newT[match]
		ch := append([]FuseEditChange{{Field: "Name", Old: oi.t.Name, New: ni.t.Name}}, diffFuseTrigger(oi.t, ni.t)...)
		triggers = append(triggers, FuseEditItem{Kind: "modified", Name: ni.t.Name, Path: ni.path, Changes: ch})
		consumedAdd[match], consumedRem[rk] = true, true
	}
	// Pass 2: moves — same name and search text in a different group.
	for _, rk := range removedKeys {
		if consumedRem[rk] {
			continue
		}
		oi := oldT[rk]
		match, matches := "", 0
		for _, ak := range addedKeys {
			if consumedAdd[ak] {
				continue
			}
			ni := newT[ak]
			if ni.t.Name == oi.t.Name && ni.t.TriggerText == oi.t.TriggerText {
				match = ak
				matches++
			}
		}
		if matches != 1 {
			continue
		}
		ni := newT[match]
		triggers = append(triggers, FuseEditItem{
			Kind: "moved", Name: ni.t.Name, Path: ni.path, OldPath: oi.path,
			Changes: diffFuseTrigger(oi.t, ni.t),
		})
		consumedAdd[match], consumedRem[rk] = true, true
	}
	for _, ak := range addedKeys {
		if !consumedAdd[ak] {
			ni := newT[ak]
			triggers = append(triggers, FuseEditItem{Kind: "added", Name: ni.t.Name, Path: ni.path})
		}
	}
	for _, rk := range removedKeys {
		if !consumedRem[rk] {
			oi := oldT[rk]
			triggers = append(triggers, FuseEditItem{Kind: "removed", Name: oi.t.Name, Path: oi.path})
		}
	}

	sortFuseEditItems(groups)
	sortFuseEditItems(triggers)
	return groups, triggers
}

func sortFuseEditItems(items []FuseEditItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		return items[i].Name < items[j].Name
	})
}

// ── field-level trigger diff ─────────────────────────────────────────────────

// diffFuseTrigger reports the user-meaningful differences between two versions
// of one trigger, with values resolved and formatted the way the edit form
// presents them (channels show their effective value; a disabled channel is
// "(none)"). Name is the matching key and is handled by the caller; GINA's
// Modified timestamp is deliberately ignored (every save stamps it).
func diffFuseTrigger(o, n *GinaTrigger) []FuseEditChange {
	var out []FuseEditChange
	add := func(field, ov, nv string) {
		if ov != nv {
			out = append(out, FuseEditChange{Field: field, Old: ov, New: nv})
		}
	}

	add("Search text", fedText(o.TriggerText), fedText(n.TriggerText))
	add("Regular expression", fedOnOff(bool(o.EnableRegex)), fedOnOff(bool(n.EnableRegex)))
	add("Comments", fedText(o.Comments), fedText(n.Comments))
	add("Overlay", fedOverlay(o.Category), fedOverlay(n.Category))

	add("Alert text", fedText(fedEffText(o)), fedText(fedEffText(n)))
	add("Speech", fedText(fedEffTTS(o)), fedText(fedEffTTS(n)))
	if fedEffTTS(o) != "" && fedEffTTS(n) != "" {
		add("Interrupt speech", fedOnOff(bool(o.InterruptSpeech)), fedOnOff(bool(n.InterruptSpeech)))
	}
	add("Sound", fedFile(fedEffSound(o)), fedFile(fedEffSound(n)))
	add("Clipboard text", fedText(fedEffClip(o)), fedText(fedEffClip(n)))
	add("Counter reset", fedCounter(o), fedCounter(n))

	ot, nt := fedIsTimer(o), fedIsTimer(n)
	// One summary line carries both the on/off toggle and a duration change.
	add("Timer", fedTimerSummary(o), fedTimerSummary(n))
	if ot && nt {
		add("Timer bar name", fedTimerName(o), fedTimerName(n))
		add("If already running", fedStartBehavior(o), fedStartBehavior(n))
		add("Show bar for last", fedVisible(o), fedVisible(n))
		add("Timer Ending warning", fedEndingSummary(o), fedEndingSummary(n))
		if fedEndingArmed(o) && fedEndingArmed(n) {
			add("Ending alert text", fedText(fedEndText(o.TimerEndingTrigger)), fedText(fedEndText(n.TimerEndingTrigger)))
			add("Ending speech", fedText(fedEndTTS(o.TimerEndingTrigger)), fedText(fedEndTTS(n.TimerEndingTrigger)))
			add("Ending sound", fedFile(fedEndSound(o.TimerEndingTrigger)), fedFile(fedEndSound(n.TimerEndingTrigger)))
		}
		add("Timer Ended actions", fedOnOff(bool(o.UseTimerEnded)), fedOnOff(bool(n.UseTimerEnded)))
		if bool(o.UseTimerEnded) && bool(n.UseTimerEnded) {
			add("Ended alert text", fedText(fedEndText(o.TimerEndedTrigger)), fedText(fedEndText(n.TimerEndedTrigger)))
			add("Ended speech", fedText(fedEndTTS(o.TimerEndedTrigger)), fedText(fedEndTTS(n.TimerEndedTrigger)))
			add("Ended sound", fedFile(fedEndSound(o.TimerEndedTrigger)), fedFile(fedEndSound(n.TimerEndedTrigger)))
		}
		add("End early conditions", fedEnders(o), fedEnders(n))
	}
	return out
}

// ── value formatting (fed = Fuse edit diff) ──────────────────────────────────

func fedOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func fedText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(none)"
	}
	return "“" + s + "”"
}

func fedFile(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func fedOverlay(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(none)"
	}
	return s
}

// Effective channel values, resolved the way the engine resolves them.
func fedEffText(t *GinaTrigger) string {
	if bool(t.UseText) {
		return strings.TrimSpace(t.DisplayText)
	}
	return ""
}

func fedEffTTS(t *GinaTrigger) string {
	if bool(t.UseTextToVoice) {
		if s := strings.TrimSpace(t.TextToVoiceText); s != "" {
			return s
		}
		return strings.TrimSpace(t.DisplayText)
	}
	return ""
}

func fedEffSound(t *GinaTrigger) string {
	if bool(t.PlayMediaFile) {
		return mediaBasename(t.MediaFileName)
	}
	return ""
}

func fedEffClip(t *GinaTrigger) string {
	if bool(t.CopyToClipboard) {
		return strings.TrimSpace(t.ClipboardText)
	}
	return ""
}

func fedCounter(t *GinaTrigger) string {
	if !bool(t.UseCounterResetTimer) || t.CounterResetDuration <= 0 {
		return "off"
	}
	return fedDur(int64(t.CounterResetDuration) * 1000)
}

func fedDurMs(t *GinaTrigger) int64 {
	d := t.TimerMillisecondDuration
	if d <= 0 {
		d = int64(t.TimerDuration) * 1000
	}
	return d
}

func fedIsTimer(t *GinaTrigger) bool {
	return t.TimerType == "Timer" && fedDurMs(t) > 0
}

func fedTimerSummary(t *GinaTrigger) string {
	if !fedIsTimer(t) {
		return "off"
	}
	return "on (" + fedDur(fedDurMs(t)) + ")"
}

func fedTimerName(t *GinaTrigger) string {
	if s := strings.TrimSpace(t.TimerName); s != "" {
		return "“" + s + "”"
	}
	return "(trigger name)"
}

func fedStartBehavior(t *GinaTrigger) string {
	b := t.TimerStartBehavior
	if bool(t.RestartBasedOnTimerName) {
		b = "RestartTimer"
	}
	switch b {
	case "RestartTimer":
		return "restart this trigger's timer"
	case "IgnoreIfRunning":
		return "ignore"
	default:
		return "start another timer"
	}
}

func fedVisible(t *GinaTrigger) string {
	if t.TimerVisibleDuration <= 0 {
		return "full duration"
	}
	return fedDur(int64(t.TimerVisibleDuration) * 1000)
}

func fedEndingArmed(t *GinaTrigger) bool {
	return bool(t.UseTimerEnding) && t.TimerEndingTime > 0
}

func fedEndingSummary(t *GinaTrigger) string {
	if !fedEndingArmed(t) {
		return "off"
	}
	return fedDur(int64(t.TimerEndingTime)*1000) + " before end"
}

func fedEndText(e *GinaEndTrigger) string {
	if e == nil || !bool(e.UseText) {
		return ""
	}
	return strings.TrimSpace(e.DisplayText)
}

func fedEndTTS(e *GinaEndTrigger) string {
	if e == nil || !bool(e.UseTextToVoice) {
		return ""
	}
	if s := strings.TrimSpace(e.TextToVoiceText); s != "" {
		return s
	}
	return strings.TrimSpace(e.DisplayText)
}

func fedEndSound(e *GinaEndTrigger) string {
	if e == nil || !bool(e.PlayMediaFile) {
		return ""
	}
	return mediaBasename(e.MediaFileName)
}

func fedEnders(t *GinaTrigger) string {
	if t.TimerEarlyEnders == nil || len(t.TimerEarlyEnders.Enders) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(t.TimerEarlyEnders.Enders))
	for _, e := range t.TimerEarlyEnders.Enders {
		s := "“" + strings.TrimSpace(e.EarlyEndText) + "”"
		if bool(e.EnableRegex) {
			s += " (regex)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// fedDur renders a millisecond duration compactly: "45s", "2m 30s", "1h 5m".
func fedDur(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	if ms%1000 != 0 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	s := ms / 1000
	h, m, sec := s/3600, (s%3600)/60, s%60
	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if sec > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", sec))
	}
	return strings.Join(parts, " ")
}

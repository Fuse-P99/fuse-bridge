package main

import (
	"encoding/json"
	"encoding/xml"
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

// The trigger tree has exactly two top-level groups:
//   - Fuse Triggers: the guild's shared set, downloaded from the server and
//     edited only by officers (edits write through to the server and propagate
//     to everyone). Cached locally so it works offline.
//   - Personal: the user's own set, local to this machine, editable by anyone.
//
// Non-linked users have only Personal.
const (
	// Stable GroupIds for the two roots (kept clear of GINA's ranges and of
	// nextGroupIDLocked's 1,000,000+ allocations).
	fuseRootGroupID     = 2000000
	personalRootGroupID = 2000001
	fuseTriggersName    = "Fuse Triggers"
	personalName        = "Personal"
)

var (
	trigStoreMu   sync.Mutex
	trigCfg       *ginaConfig          // assembled from fuseRoot + personalRoot
	trigByID      map[int]*GinaTrigger // session trigger id → trigger
	trigGroupOf   map[int]*GinaGroup   // session trigger id → containing group
	groupByID     map[int]*GinaGroup   // GroupId → group
	groupParentOf map[int]*GinaGroup   // GroupId → parent group (nil for roots)
	trigNextID    int                  // next session trigger id

	fuseRoot     *GinaGroup // "Fuse Triggers" subtree (nil until linked+loaded)
	personalRoot *GinaGroup // "Personal" subtree (always present)
	fuseVersion  int        // server version of the cached Fuse set (0 = unseeded)

	// User on/off toggles from the edit tree, layered over the rule-based
	// defaults. Groups key by GroupId; triggers by "GroupId/Name" (triggers
	// have no persistent id of their own). Persisted in trigger_toggles.json.
	trigGroupToggle map[int]bool
	trigTrigToggle  map[string]bool
)

func triggersDir() string { return filepath.Dir(settingsPath()) }

// legacyTriggersPath is the pre-server GINA import copy (migrated on first run).
func legacyTriggersPath() string { return filepath.Join(triggersDir(), "triggers.xml") }

// fuseCachePath caches the server's Fuse Triggers set (version + XML) so it
// works offline and shows instantly before the sync completes.
func fuseCachePath() string { return filepath.Join(triggersDir(), "fuse_triggers.json") }

// personalPath stores the user's local Personal trigger subtree.
func personalPath() string { return filepath.Join(triggersDir(), "personal_triggers.xml") }

// fuseCacheFile is the on-disk form of the cached Fuse Triggers set.
type fuseCacheFile struct {
	Version int    `json:"version"`
	XML     string `json:"xml"`
}

// LoadTriggers assembles the trigger tree from the local Personal file and the
// cached Fuse set, migrating a legacy GINA import on first run, then rebuilds
// the engine's active set. The server sync (SyncFuseTriggers) runs separately.
func LoadTriggers() {
	trigStoreMu.Lock()
	migrateLegacyLocked()
	if personalRoot == nil {
		personalRoot = loadPersonalLocked()
	}
	if fuseRoot == nil {
		fuseRoot, fuseVersion = loadFuseCacheLocked()
	}
	assembleLocked()
	loadTrigTogglesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
}

// assembleLocked rebuilds trigCfg from fuseRoot (only when linked) + personalRoot
// and refreshes the lookup indexes. Caller holds trigStoreMu.
func assembleLocked() {
	if personalRoot == nil {
		personalRoot = newPersonalRoot()
	}
	var groups []*GinaGroup
	if IsLinked() && fuseRoot != nil {
		fuseRoot.Name = fuseTriggersName // enforce the rename regardless of source
		fuseRoot.GroupID = fuseRootGroupID
		groups = append(groups, fuseRoot)
	}
	groups = append(groups, personalRoot)
	trigCfg = &ginaConfig{Groups: groups}
	rebuildTriggerIndexLocked()
}

func newPersonalRoot() *GinaGroup {
	return &GinaGroup{Name: personalName, GroupID: personalRootGroupID}
}

// loadPersonalLocked reads the Personal subtree, or returns a fresh empty one.
func loadPersonalLocked() *GinaGroup {
	data, err := os.ReadFile(personalPath())
	if err != nil {
		return newPersonalRoot()
	}
	var g GinaGroup
	if xml.Unmarshal(data, &g) != nil {
		return newPersonalRoot()
	}
	g.Name = personalName
	g.GroupID = personalRootGroupID
	return &g
}

// loadFuseCacheLocked reads the cached Fuse set + its version, or (nil, 0).
func loadFuseCacheLocked() (*GinaGroup, int) {
	data, err := os.ReadFile(fuseCachePath())
	if err != nil {
		return nil, 0
	}
	var f fuseCacheFile
	if json.Unmarshal(data, &f) != nil || f.XML == "" {
		return nil, 0
	}
	var g GinaGroup
	if xml.Unmarshal([]byte(f.XML), &g) != nil {
		return nil, 0
	}
	return &g, f.Version
}

// migrateLegacyLocked one-time-migrates a pre-server triggers.xml: the "Fuse"
// package becomes the seed Fuse set (renamed "Fuse Triggers"); every other
// top-level group moves under Personal. The legacy file is then retired so this
// runs only once. No-op if already migrated or nothing to migrate.
func migrateLegacyLocked() {
	if fuseRoot != nil || personalRoot != nil {
		return
	}
	data, err := os.ReadFile(legacyTriggersPath())
	if err != nil {
		return
	}
	var cfg ginaConfig
	if xml.Unmarshal(data, &cfg) != nil {
		return
	}
	personal := newPersonalRoot()
	for _, g := range cfg.Groups {
		if fuseRoot == nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(g.Name)), "fuse") {
			g.Name = fuseTriggersName
			g.GroupID = fuseRootGroupID
			fuseRoot = g
			continue
		}
		personal.Groups = append(personal.Groups, g)
	}
	personalRoot = personal
	fuseVersion = 0 // unseeded locally; an officer will publish it on first sync
	_ = savePersonalLocked()
	if fuseRoot != nil {
		_ = saveFuseCacheLocked()
	}
	// Retire the legacy file so we don't migrate again.
	_ = os.Rename(legacyTriggersPath(), legacyTriggersPath()+".migrated")
	addStatus("Triggers: migrated your imported set to Fuse Triggers + Personal")
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

// effectiveGroupEnabledLocked returns whether a group is enabled: the user's
// slider override wins; otherwise the rule-based default (see
// defaultGroupEnabledLocked). Caller holds trigStoreMu.
func effectiveGroupEnabledLocked(g *GinaGroup) bool {
	if v, ok := trigGroupToggle[g.GroupID]; ok {
		return v
	}
	return defaultGroupEnabledLocked(g)
}

// effectiveTriggerEnabledLocked: triggers default to on; the slider overrides.
func effectiveTriggerEnabledLocked(g *GinaGroup, t *GinaTrigger) bool {
	if v, ok := trigTrigToggle[trigToggleKey(g, t)]; ok {
		return v
	}
	return true
}

// defaultGroupEnabledLocked is the enablement for a group with no user override.
// Personal is on. In Fuse Triggers, the "03 - Buffs", "04 - Debuffs" and
// "05 - Raiding" sections are on for everyone; "01 - Class Specific" is on only
// for the current character's own class subsection; everything else is off.
func defaultGroupEnabledLocked(g *GinaGroup) bool {
	// The two container roots are always "on" (they hold no triggers of their
	// own; their children control what actually fires).
	if g.GroupID == fuseRootGroupID || g.GroupID == personalRootGroupID {
		return true
	}
	if isPersonalGroupLocked(g.GroupID) {
		return true
	}
	section, underClass := fuseSectionOfLocked(g)
	if section == nil {
		return false
	}
	switch {
	case sectionEnabledForAll(section.Name):
		return true
	case sectionIsClassSpecific(section.Name):
		class := classForCurrentChar()
		return class != "" && underClass
	default:
		return false
	}
}

// fuseSectionOfLocked walks up from g to the Fuse root, returning the top
// section (direct child of the Fuse root) g lives under, and whether the path
// passed through a subgroup named for the current character's class. Returns
// (nil, false) if g is the Fuse root itself or not in the Fuse subtree.
func fuseSectionOfLocked(g *GinaGroup) (section *GinaGroup, underClass bool) {
	class := classForCurrentChar()
	var chain []*GinaGroup
	for cur := g; cur != nil && cur.GroupID != fuseRootGroupID; cur = groupParentOf[cur.GroupID] {
		chain = append(chain, cur)
	}
	if len(chain) == 0 {
		return nil, false
	}
	// Confirm we actually reached the Fuse root (topmost's parent is it).
	top := chain[len(chain)-1]
	if p := groupParentOf[top.GroupID]; p == nil || p.GroupID != fuseRootGroupID {
		return nil, false
	}
	for _, c := range chain {
		if class != "" && strings.EqualFold(strings.TrimSpace(c.Name), class) {
			underClass = true
		}
	}
	return top, underClass
}

func sectionEnabledForAll(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, p := range []string{"03 -", "04 -", "05 -"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return strings.HasPrefix(n, "buffs") || strings.HasPrefix(n, "debuffs") ||
		strings.HasPrefix(n, "raiding")
}

func sectionIsClassSpecific(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(n, "01 -") || strings.Contains(n, "class specific")
}

// isPersonalGroupLocked reports whether a group is the Personal root or under it.
func isPersonalGroupLocked(groupID int) bool {
	for cur := groupByID[groupID]; cur != nil; cur = groupParentOf[cur.GroupID] {
		if cur.GroupID == personalRootGroupID {
			return true
		}
	}
	return false
}

// isFuseGroupLocked reports whether a group is the Fuse root or under it.
func isFuseGroupLocked(groupID int) bool {
	for cur := groupByID[groupID]; cur != nil; cur = groupParentOf[cur.GroupID] {
		if cur.GroupID == fuseRootGroupID {
			return true
		}
	}
	return false
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

// saveTriggersLocked persists both local subtrees (Personal file + Fuse cache).
// The Fuse set's write-through to the server is handled separately by the edit
// bindings (officer-only). Kept as one call so existing mutation paths persist
// everything without caring which subtree changed.
func saveTriggersLocked() error {
	if err := savePersonalLocked(); err != nil {
		return err
	}
	return saveFuseCacheLocked()
}

// savePersonalLocked writes the Personal subtree to disk (atomically).
func savePersonalLocked() error {
	if personalRoot == nil {
		return nil
	}
	body, err := marshalGroupXML(personalRoot)
	if err != nil {
		return err
	}
	return atomicWrite(personalPath(), append([]byte(xml.Header), body...))
}

// saveFuseCacheLocked writes the cached Fuse set (version + XML) to disk.
func saveFuseCacheLocked() error {
	if fuseRoot == nil {
		return nil
	}
	body, err := marshalGroupXML(fuseRoot)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(fuseCacheFile{Version: fuseVersion, XML: string(body)}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(fuseCachePath(), data)
}

// marshalGroupXML serializes one group subtree as a <TriggerGroup> element
// (matching GINA's element name, so it round-trips through xml.Unmarshal).
func marshalGroupXML(g *GinaGroup) ([]byte, error) {
	return xml.MarshalIndent(struct {
		XMLName xml.Name `xml:"TriggerGroup"`
		*GinaGroup
	}{GinaGroup: g}, "", "  ")
}

func atomicWrite(dst string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
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

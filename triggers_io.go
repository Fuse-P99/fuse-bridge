package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Fuse Triggers XML round-trip: export the shared set to a file, edit it in a
// text editor, import it back.
//
// This exists because bulk editing is genuinely easier in a text editor than in
// a tree UI, but editing the app's own storage in place would mean two writers
// on one file — the app rewrites fuse_triggers.json on every edit, on media
// localization at startup, and whenever the set is marked dirty. An explicit
// export/import makes the handoff a transaction instead of a race, and gives us
// somewhere to VALIDATE: a malformed file is rejected loudly here, where the
// normal load path would just silently drop the whole Fuse set.
//
// Import never publishes. It lands as unpublished officer edits, so the usual
// publish bar (with its "another officer published in the meantime" warning) is
// still the only thing that changes what the guild sees.

// maxPreviewPaths bounds the per-section lists shown in the import preview; the
// counts are always exact, only the listing is truncated.
const maxPreviewPaths = 40

// FuseImportPreview is the dry-run summary shown before an import is committed.
type FuseImportPreview struct {
	Valid    bool   `json:"valid"`
	Error    string `json:"error"`
	Path     string `json:"path"`
	Groups   int    `json:"groups"`
	Triggers int    `json:"triggers"`

	AddedCount   int      `json:"added_count"`
	RemovedCount int      `json:"removed_count"`
	ChangedCount int      `json:"changed_count"`
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
	Changed      []string `json:"changed"`

	// Orphaned counts per-character enable/disable overrides that point at a
	// Fuse group or trigger this file no longer defines. Those settings are
	// keyed by GroupId and by "GroupId/Name", so a bulk rename or renumber
	// silently detaches them and the affected toons fall back to defaults.
	Orphaned int `json:"orphaned"`
}

// parseFuseXML reads an exported Fuse set. A file saved by ExportFuseTriggers is
// a bare <TriggerGroup>, but a full GINA <Configuration> is accepted too so an
// officer can hand us something straight out of GINA without reshaping it.
func parseFuseXML(data []byte) (*GinaGroup, error) {
	var g GinaGroup
	if xml.Unmarshal(data, &g) == nil && countGroupTriggers(&g) > 0 {
		return &g, nil
	}

	var cfg ginaConfig
	if xml.Unmarshal(data, &cfg) != nil || len(cfg.Groups) == 0 {
		return nil, fmt.Errorf("this file isn't a trigger set the app can read (expected a <TriggerGroup> or a GINA <Configuration>)")
	}
	// Prefer a group that names itself Fuse; otherwise only an unambiguous
	// single-group file can be taken as "the Fuse set".
	for _, c := range cfg.Groups {
		if strings.Contains(strings.ToLower(c.Name), "fuse") && countGroupTriggers(c) > 0 {
			return c, nil
		}
	}
	var withTriggers []*GinaGroup
	for _, c := range cfg.Groups {
		if countGroupTriggers(c) > 0 {
			withTriggers = append(withTriggers, c)
		}
	}
	if len(withTriggers) == 1 {
		return withTriggers[0], nil
	}
	if len(withTriggers) == 0 {
		return nil, fmt.Errorf("this file contains no triggers")
	}
	return nil, fmt.Errorf("this file has %d top-level groups and none is named Fuse — export the Fuse set first and edit that", len(withTriggers))
}

// countGroups returns the number of groups in a subtree, including the root.
func countGroups(g *GinaGroup) int {
	if g == nil {
		return 0
	}
	n := 1
	for _, c := range g.Groups {
		n += countGroups(c)
	}
	return n
}

// triggerFingerprints maps "Group > Subgroup > Trigger" to a full serialization
// of the trigger, so the preview can tell an edit apart from a move or a rename.
func triggerFingerprints(root *GinaGroup) map[string]string {
	out := map[string]string{}
	if root == nil {
		return out
	}
	var walk func(g *GinaGroup, prefix string)
	walk = func(g *GinaGroup, prefix string) {
		path := g.Name
		if prefix != "" {
			path = prefix + " > " + g.Name
		}
		for _, t := range g.Triggers {
			body, _ := xml.Marshal(t)
			out[path+" > "+t.Name] = string(body)
		}
		for _, c := range g.Groups {
			walk(c, path)
		}
	}
	walk(root, "")
	return out
}

// toggleKeySets returns the group ids and "GroupId/Name" trigger keys a subtree
// defines — the exact identifiers trigger_toggles.json stores per character.
func toggleKeySets(root *GinaGroup) (map[int]bool, map[string]bool) {
	groups := map[int]bool{}
	trigs := map[string]bool{}
	if root == nil {
		return groups, trigs
	}
	var walk func(g *GinaGroup)
	walk = func(g *GinaGroup) {
		groups[g.GroupID] = true
		for _, t := range g.Triggers {
			trigs[trigToggleKey(g, t)] = true
		}
		for _, c := range g.Groups {
			walk(c)
		}
	}
	walk(root)
	return groups, trigs
}

// orphanedTogglesLocked counts stored overrides that resolve against the current
// Fuse set but would stop resolving against next. Caller holds trigStoreMu.
func orphanedTogglesLocked(next *GinaGroup) int {
	curG, curT := toggleKeySets(fuseRoot)
	newG, newT := toggleKeySets(next)
	n := 0
	forEachTrigToggleSetLocked(func(s *trigToggleSet) {
		for id := range s.Groups {
			if curG[id] && !newG[id] {
				n++
			}
		}
		for k := range s.Triggers {
			if curT[k] && !newT[k] {
				n++
			}
		}
	})
	return n
}

// sortedCapped sorts paths and returns at most maxPreviewPaths of them.
func sortedCapped(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > maxPreviewPaths {
		out = out[:maxPreviewPaths]
	}
	return out
}

// ── bindings ─────────────────────────────────────────────────────────────────

// ExportFuseTriggers writes the current Fuse Triggers set to a file the user
// picks, as plain GINA XML. Returns the path written, or "" if cancelled.
func (a *App) ExportFuseTriggers() (string, error) {
	if v3App == nil {
		return "", fmt.Errorf("unavailable")
	}
	trigStoreMu.Lock()
	var body []byte
	var err error
	version := fuseVersion
	dirty := fuseDirty
	if fuseRoot != nil {
		body, err = marshalGroupXML(fuseRoot)
	}
	trigStoreMu.Unlock()
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", fmt.Errorf("there is no Fuse Triggers set to export")
	}

	name := fmt.Sprintf("FuseTriggers-v%d.xml", version)
	if dirty {
		name = fmt.Sprintf("FuseTriggers-v%d-edited.xml", version)
	}
	// SetOptions rather than the builder chain: SaveFileDialogStruct exposes no
	// SetTitle, and the Windows implementation reads the title field.
	dlg := v3App.Dialog.SaveFile()
	dlg.SetOptions(&application.SaveFileDialogOptions{
		Title:                "Export Fuse Triggers",
		Filename:             name,
		CanCreateDirectories: true,
		Filters:              []application.FileFilter{{DisplayName: "Trigger XML", Pattern: "*.xml"}},
	})
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil // cancelled
	}
	if !strings.EqualFold(filepath.Ext(path), ".xml") {
		path += ".xml"
	}

	// The comment is for whoever opens the file; XML comments don't survive the
	// import round trip, so nothing depends on it.
	header := fmt.Sprintf("%s<!-- Fuse Triggers v%d exported %s. Edit here, then Import in Manage Timers. -->\n",
		xml.Header, version, time.Now().Format("2006-01-02 15:04"))
	if err := os.WriteFile(path, append([]byte(header), body...), 0600); err != nil {
		return "", err
	}
	addStatus("Triggers: exported Fuse Triggers v%d to %s", version, filepath.Base(path))
	return path, nil
}

// BrowseFuseTriggersXML opens a file picker for an exported trigger XML.
// Returns "" if the user cancels.
func (a *App) BrowseFuseTriggersXML() (string, error) {
	if v3App == nil {
		return "", fmt.Errorf("unavailable")
	}
	return v3App.Dialog.OpenFile().
		SetTitle("Import Fuse Triggers from XML").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("Trigger XML", "*.xml").
		PromptForSingleSelection()
}

// PreviewFuseTriggersImport parses a file and reports what importing it would
// change, without touching anything. Every failure mode is reported as text so
// the dialog can show it — this is the step that stops a typo from quietly
// emptying the Fuse set.
func (a *App) PreviewFuseTriggersImport(path string) FuseImportPreview {
	if strings.TrimSpace(path) == "" {
		return FuseImportPreview{Error: "No file selected."}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FuseImportPreview{Error: "Could not read file: " + err.Error()}
	}
	next, err := parseFuseXML(data)
	if err != nil {
		return FuseImportPreview{Error: err.Error()}
	}
	total := countGroupTriggers(next)
	if total == 0 {
		return FuseImportPreview{Error: "This file defines no triggers — refusing to replace the Fuse set with an empty one."}
	}

	trigStoreMu.Lock()
	cur := triggerFingerprints(fuseRoot)
	orphans := orphanedTogglesLocked(next)
	trigStoreMu.Unlock()

	incoming := triggerFingerprints(next)
	added, removed, changed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for k, v := range incoming {
		if old, ok := cur[k]; !ok {
			added[k] = true
		} else if old != v {
			changed[k] = true
		}
	}
	for k := range cur {
		if _, ok := incoming[k]; !ok {
			removed[k] = true
		}
	}

	return FuseImportPreview{
		Valid:        true,
		Path:         path,
		Groups:       countGroups(next),
		Triggers:     total,
		AddedCount:   len(added),
		RemovedCount: len(removed),
		ChangedCount: len(changed),
		Added:        sortedCapped(added),
		Removed:      sortedCapped(removed),
		Changed:      sortedCapped(changed),
		Orphaned:     orphans,
	}
}

// ImportFuseTriggersXML replaces the local Fuse Triggers set with the contents
// of path. Officer-only, and deliberately NOT a publish: the set lands dirty so
// the existing publish bar is still what pushes it to the guild (and still warns
// if another officer published in the meantime).
func (a *App) ImportFuseTriggersXML(path string) error {
	if !IsLinked() {
		return fmt.Errorf("not linked to a Discord account")
	}
	refreshOfficerStatus()
	if !isOfficerCached() {
		return fmt.Errorf("only officers can import Fuse Triggers")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, err := parseFuseXML(data)
	if err != nil {
		return err
	}
	count := countGroupTriggers(next)
	if count == 0 {
		return fmt.Errorf("this file defines no triggers")
	}

	next.Name = fuseTriggersName
	next.GroupID = fuseRootGroupID

	trigStoreMu.Lock()
	fuseRoot = next
	fuseDirty = true // unpublished until the officer says otherwise
	scrubMediaNamesInGroup(fuseRoot)
	assembleLocked()
	// Pull in any audio the imported triggers reference that we already have on
	// disk, and reduce machine-specific paths to bare file names.
	localizeAndNormalizeMediaLocked()
	err = saveFuseCacheLocked()
	trigStoreMu.Unlock()
	if err != nil {
		return err
	}

	RebuildTriggerActivation()
	addStatus("Triggers: imported %d Fuse triggers from %s — not published yet", count, filepath.Base(path))
	return nil
}

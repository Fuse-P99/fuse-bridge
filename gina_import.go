package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GINA import: let the user pull selected top-level trigger groups out of their
// own GINAConfig.xml into their Personal set. The guild's shared "Fuse"/"Riot"
// groups are excluded — those ship in the distributed Fuse Triggers package.

// ginaExcludedGroup reports whether a top-level group name is one we never
// import (it's provided by the shared Fuse Triggers set instead).
func ginaExcludedGroup(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "fuse") || strings.Contains(n, "riot")
}

func countGroupTriggers(g *GinaGroup) int {
	if g == nil {
		return 0
	}
	n := len(g.Triggers)
	for _, c := range g.Groups {
		n += countGroupTriggers(c)
	}
	return n
}

// GINAGroupOption is one top-level trigger group offered for import.
type GINAGroupOption struct {
	GroupID  int    `json:"group_id"`
	Name     string `json:"name"`
	Triggers int    `json:"triggers"` // total in the subtree
	Excluded bool   `json:"excluded"` // Fuse/Riot — shown but not selectable
}

// GINAScanResult is what the import dialog shows after reading a GINAConfig.xml.
type GINAScanResult struct {
	Valid  bool              `json:"valid"`
	Error  string            `json:"error"`
	Groups []GINAGroupOption `json:"groups"`
}

// DefaultGINAConfigPath returns GINA's usual config location for this user
// (%LOCALAPPDATA%\GimaSoft\GINA\GINAConfig.xml), so the dialog can pre-fill it.
func (a *App) DefaultGINAConfigPath() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "GimaSoft", "GINA", "GINAConfig.xml")
}

// BrowseGINAConfig opens a file picker for a GINAConfig.xml, starting in GINA's
// folder when it exists. Returns "" if the user cancels.
func (a *App) BrowseGINAConfig() (string, error) {
	if v3App == nil {
		return "", fmt.Errorf("unavailable")
	}
	d := v3App.Dialog.OpenFile().
		SetTitle("Select your GINA configuration (GINAConfig.xml)").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("GINA config", "*.xml")
	if dir := filepath.Dir(a.DefaultGINAConfigPath()); dir != "" {
		if _, err := os.Stat(dir); err == nil {
			d = d.SetDirectory(dir)
		}
	}
	path, err := d.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return path, nil
}

// ScanGINAConfig parses a GINAConfig.xml and lists its top-level trigger groups
// for the import dialog. Fuse/Riot groups are flagged excluded.
func (a *App) ScanGINAConfig(path string) GINAScanResult {
	if strings.TrimSpace(path) == "" {
		return GINAScanResult{Error: "No file selected."}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GINAScanResult{Error: "Could not read file: " + err.Error()}
	}
	var cfg ginaConfig
	if xml.Unmarshal(data, &cfg) != nil || len(cfg.Groups) == 0 {
		return GINAScanResult{Error: "This doesn't look like a GINA configuration (no trigger groups found)."}
	}
	res := GINAScanResult{Valid: true}
	for _, g := range cfg.Groups {
		res.Groups = append(res.Groups, GINAGroupOption{
			GroupID:  g.GroupID,
			Name:     g.Name,
			Triggers: countGroupTriggers(g),
			Excluded: ginaExcludedGroup(g.Name),
		})
	}
	return res
}

// ImportGINAGroups imports the chosen top-level groups (by GroupId) from a
// GINAConfig.xml into the Personal set, localizing their audio and scrubbing
// paths, then rebuilds the active set. Returns how many groups were imported.
func (a *App) ImportGINAGroups(path string, groupIDs []int) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var cfg ginaConfig
	if xml.Unmarshal(data, &cfg) != nil {
		return 0, fmt.Errorf("not a valid GINA configuration")
	}
	want := make(map[int]bool, len(groupIDs))
	for _, id := range groupIDs {
		want[id] = true
	}

	trigStoreMu.Lock()
	if personalRoot == nil {
		personalRoot = newPersonalRoot()
	}
	// Reassign IDs above the current maximum so imported groups can't collide with
	// existing ones (GINA's ranges overlap our own allocations).
	next := nextGroupIDLocked()
	imported := 0
	for _, g := range cfg.Groups {
		if !want[g.GroupID] || ginaExcludedGroup(g.Name) {
			continue
		}
		reassignGroupIDs(g, &next)
		personalRoot.Groups = append(personalRoot.Groups, g)
		imported++
	}
	if imported > 0 {
		assembleLocked()                  // rebuild trigCfg + assign trigger session IDs
		localizeAndNormalizeMediaLocked() // copy the imported groups' audio + scrub paths
		_ = saveTriggersLocked()
	}
	trigStoreMu.Unlock()

	if imported > 0 {
		RebuildTriggerActivation()
	}
	return imported, nil
}

// reassignGroupIDs walks a group subtree giving each group a fresh unique id,
// bumping *next as it goes. Caller holds trigStoreMu.
func reassignGroupIDs(g *GinaGroup, next *int) {
	g.GroupID = *next
	*next++
	for _, c := range g.Groups {
		reassignGroupIDs(c, next)
	}
}

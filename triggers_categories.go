package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Category presentation — the color, opacity, and font each timer-bar / text-alert
// category is drawn with, plus any category the user created that no trigger
// references yet.
//
// Stored app-wide rather than per character: a category is a shared concept
// (its triggers live in the guild's Fuse set), so it should look the same on
// every toon and in every overlay. Per-character storage covers overlay
// geometry and which triggers are enabled — not what a category looks like.
//
// A category's *name* lives on the triggers themselves (GinaTrigger.Category),
// so renaming one rewrites every trigger that references it. Style is keyed by
// kind as well as name, because the same category can feed both a timer-bar
// overlay and a text-alert overlay and those want different treatment.

type CategoryStyle struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "timers" | "alerts"
	// Timer bars: the fill and the empty-track behind it. Text alerts reuse
	// Bg* as the panel behind the text and ignore Bar*.
	BarColor   string  `json:"bar_color"`
	BarOpacity float64 `json:"bar_opacity"`
	BgColor    string  `json:"bg_color"`
	BgOpacity  float64 `json:"bg_opacity"`
	FontFamily string  `json:"font_family"` // "" = inherit the shell font
	FontColor  string  `json:"font_color"`
	FontSize   int     `json:"font_size"`
}

var (
	catStyleMu sync.Mutex
	// key: kind + "|" + lower(name). Presence also marks a category as
	// explicit — it shows on the Manage Overlays page even with no triggers.
	catStyles = map[string]*CategoryStyle{}
)

// catPalette mirrors PALETTE in frontend/src/lib/catColor.js. Both sides must
// agree: the frontend still falls back to its own hash for a category whose
// style hasn't loaded yet, and a mismatch would make colors jump on load.
var catPalette = []string{
	"#c8a951", // gold (accent)
	"#4fb3a9", // teal
	"#6b9bd1", // steel blue
	"#a58fd6", // violet
	"#d1706b", // brick
	"#7fb069", // moss
	"#d19a5b", // amber
	"#c67fb0", // rose
	"#5bbcd1", // cyan
	"#a9b05f", // olive
}

// paletteColor is catColor() from catColor.js, ported verbatim (h*31 + char,
// wrapped to uint32) so a category lands on the same hue in both languages.
func paletteColor(name string) string {
	var h uint32
	for _, r := range name {
		h = h*31 + uint32(r)
	}
	return catPalette[int(h)%len(catPalette)]
}

func catStyleKey(kind, name string) string {
	return kind + "|" + strings.ToLower(strings.TrimSpace(name))
}

func catStylesPath() string { return filepath.Join(triggersDir(), "trigger_categories.json") }

// defaultCatStyle is what a category looks like before anyone customizes it —
// the same look the overlays shipped with, so existing setups don't change.
func defaultCatStyle(kind, name string) CategoryStyle {
	s := CategoryStyle{
		Name:       name,
		Kind:       kind,
		BarColor:   paletteColor(name),
		BarOpacity: 0.82,
		BgColor:    "#000000",
		BgOpacity:  0,
		FontColor:  "#ffffff",
		FontSize:   12,
	}
	if kind == "alerts" {
		// Alert text is drawn straight onto the game with no track behind it,
		// so it runs bigger and takes its color from the category.
		s.FontColor = paletteColor(name)
		s.FontSize = 16
	}
	return s
}

// resolveCatStyle merges any stored customization over the defaults.
func resolveCatStyle(kind, name string) CategoryStyle {
	if strings.TrimSpace(name) == "" {
		name = "Default"
	}
	out := defaultCatStyle(kind, name)
	catStyleMu.Lock()
	defer catStyleMu.Unlock()
	s := catStyles[catStyleKey(kind, name)]
	if s == nil {
		return out
	}
	if s.BarColor != "" {
		out.BarColor = s.BarColor
	}
	if s.BarOpacity >= 0 {
		out.BarOpacity = s.BarOpacity
	}
	if s.BgColor != "" {
		out.BgColor = s.BgColor
	}
	if s.BgOpacity >= 0 {
		out.BgOpacity = s.BgOpacity
	}
	if s.FontFamily != "" {
		out.FontFamily = s.FontFamily
	}
	if s.FontColor != "" {
		out.FontColor = s.FontColor
	}
	if s.FontSize > 0 {
		out.FontSize = s.FontSize
	}
	return out
}

func loadCatStyles() {
	catStyleMu.Lock()
	defer catStyleMu.Unlock()
	catStyles = map[string]*CategoryStyle{}
	data, err := os.ReadFile(catStylesPath())
	if err != nil {
		return
	}
	var f struct {
		Cats map[string]*CategoryStyle `json:"cats"`
	}
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for k, s := range f.Cats {
		if s != nil {
			catStyles[k] = s
		}
	}
}

func saveCatStylesLocked() error {
	data, err := json.MarshalIndent(struct {
		Cats map[string]*CategoryStyle `json:"cats"`
	}{Cats: catStyles}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(catStylesPath(), data)
}

// ── bindings ────────────────────────────────────────────────────────────────

// GetCategoryStyle returns the resolved look for one category, for the popout
// overlays (which render a single category and don't need the full inventory).
func (a *App) GetCategoryStyle(kind, name string) CategoryStyle {
	return resolveCatStyle(kind, name)
}

// CreateTriggerCategory registers a category with no triggers in it yet, so it
// can be styled and popped out before anything is assigned to it. The name only
// becomes real on a trigger once the user sets that trigger's Category.
func (a *App) CreateTriggerCategory(kind, name, color string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if kind != "timers" && kind != "alerts" {
		return fmt.Errorf("unknown category kind %q", kind)
	}
	catStyleMu.Lock()
	defer catStyleMu.Unlock()
	key := catStyleKey(kind, name)
	if catStyles[key] != nil {
		return fmt.Errorf("a %s category named %q already exists", kindLabel(kind), name)
	}
	s := defaultCatStyle(kind, name)
	if strings.TrimSpace(color) != "" {
		s.BarColor = color
		if kind == "alerts" {
			s.FontColor = color
		}
	}
	catStyles[key] = &s
	return saveCatStylesLocked()
}

func kindLabel(kind string) string {
	if kind == "alerts" {
		return "text alert"
	}
	return "timer bar"
}

// SaveTriggerCategory applies an edited style and, when the name changed,
// rewrites Category on every trigger that referenced the old name.
//
// The rename spans both kinds: Category is a single field on the trigger, so a
// category feeding both bars and alerts is one category with two looks. The
// style records for both kinds move with it.
func (a *App) SaveTriggerCategory(oldName string, in CategoryStyle) error {
	newName := strings.TrimSpace(in.Name)
	if newName == "" {
		return fmt.Errorf("name is required")
	}
	if in.Kind != "timers" && in.Kind != "alerts" {
		return fmt.Errorf("unknown category kind %q", in.Kind)
	}
	oldName = strings.TrimSpace(oldName)

	renamed := !strings.EqualFold(oldName, newName)
	if renamed && oldName != "" {
		if err := renameTriggerCategory(oldName, newName); err != nil {
			return err
		}
	}

	catStyleMu.Lock()
	if renamed && oldName != "" {
		// Carry both kinds' styles to the new name so the sibling overlay
		// doesn't silently revert to palette defaults.
		for _, k := range []string{"timers", "alerts"} {
			from, to := catStyleKey(k, oldName), catStyleKey(k, newName)
			if s := catStyles[from]; s != nil {
				s.Name = newName
				catStyles[to] = s
				delete(catStyles, from)
			}
		}
	}
	s := in
	s.Name = newName
	catStyles[catStyleKey(in.Kind, newName)] = &s
	err := saveCatStylesLocked()
	catStyleMu.Unlock()

	emitTriggersChanged()
	return err
}

// DeleteTriggerCategory drops a category's style records and moves its triggers
// to reassignTo — or clears their Category when reassignTo is empty, leaving
// them uncategorized (flagged as needing configuration in the trigger lists).
func (a *App) DeleteTriggerCategory(name, reassignTo string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if err := renameTriggerCategory(name, strings.TrimSpace(reassignTo)); err != nil {
		return err
	}
	catStyleMu.Lock()
	for _, k := range []string{"timers", "alerts"} {
		delete(catStyles, catStyleKey(k, name))
	}
	err := saveCatStylesLocked()
	catStyleMu.Unlock()

	emitTriggersChanged()
	return err
}

// renameTriggerCategory rewrites Category on every trigger currently set to
// from (case-insensitive), to to. An empty to clears the field.
//
// Editing a trigger in the Fuse subtree is officer-only, so this refuses up
// front if any affected trigger is a Fuse one and the user isn't an officer —
// a partial rename would leave the category split in two.
func renameTriggerCategory(from, to string) error {
	if strings.TrimSpace(from) == "" {
		return nil
	}
	trigStoreMu.Lock()
	if trigCfg == nil {
		trigStoreMu.Unlock()
		return nil
	}

	var affected []*GinaTrigger
	fuseTouched := false
	for id, t := range trigByID {
		if !strings.EqualFold(strings.TrimSpace(t.Category), from) {
			continue
		}
		affected = append(affected, t)
		if g := trigGroupOf[id]; g != nil && isFuseGroupLocked(g.GroupID) {
			fuseTouched = true
		}
	}
	if len(affected) == 0 {
		trigStoreMu.Unlock()
		return nil
	}
	if fuseTouched && !isOfficerCached() {
		trigStoreMu.Unlock()
		return fmt.Errorf("only officers can rename or delete a category used by Fuse Triggers")
	}
	for _, t := range affected {
		t.Category = to
	}
	err := saveTriggersLocked()
	trigStoreMu.Unlock()

	if err != nil {
		return err
	}
	if fuseTouched {
		markFuseDirty()
	}
	// Recompiles the active set so the category change takes effect on the live
	// board. Takes trigStoreMu, so it runs after the unlock above.
	RebuildTriggerActivation()
	return nil
}

// knownCategoryNames lists every category name the trigger set references,
// regardless of enablement — the reassign dropdown has to offer categories the
// current character has switched off.
func knownCategoryNames() []string {
	seen := map[string]string{}
	trigStoreMu.Lock()
	for _, t := range trigByID {
		if c := strings.TrimSpace(t.Category); c != "" {
			seen[strings.ToLower(c)] = c
		}
	}
	trigStoreMu.Unlock()
	catStyleMu.Lock()
	for _, s := range catStyles {
		if c := strings.TrimSpace(s.Name); c != "" {
			seen[strings.ToLower(c)] = c
		}
	}
	catStyleMu.Unlock()

	out := make([]string, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// GetCategoryNames backs the "reassign to" dropdown on category delete.
func (a *App) GetCategoryNames() []string { return knownCategoryNames() }

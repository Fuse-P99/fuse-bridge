package main

import (
	"fmt"
	"sync"
)

// "View as": an admin-only preview that makes the whole app behave as a
// different persona, for checking what each kind of user actually sees.
//
//	"unlinked"  — IsLinked() reads false everywhere: no Fuse Triggers, no
//	              server boards, no forwarding; the Personal folder and local
//	              features keep working. (Your local data stays visible — this
//	              previews the unlinked STATE, not a fresh install.)
//	"noconfig"  — linked member who has never configured timers: the Personal
//	              set shows empty and every trigger toggle reads its default.
//	              Edits made in this mode land on session-only throwaways and
//	              are NEVER persisted — the real config is untouched.
//	"linked"    — a regular linked member: your data, but officer and admin
//	              privileges dropped (Fuse set read-only, gated tabs hidden).
//
// All three also force IsAdminMode()/IsOfficer() false, so admin/officer UI
// disappears exactly as it would for that user. The one deliberate exception
// is the View-as control itself, which the frontend gates on GetViewAs()'s
// RealAdmin — otherwise entering a preview would hide the way back out.
//
// In-memory only: never persisted, so a restart always comes back as yourself.

var (
	viewAsMu    sync.Mutex
	viewAsLevel string // "" = off
)

// ViewAsActive returns the current preview level ("" when off).
func ViewAsActive() string {
	viewAsMu.Lock()
	defer viewAsMu.Unlock()
	return viewAsLevel
}

// viewAsLabels maps levels to display names (also the validation set).
var viewAsLabels = map[string]string{
	"unlinked": "Unlinked User",
	"noconfig": "Linked User — No Config",
	"linked":   "Linked User",
}

// ViewAsUI is the state the frontend's View-as control renders from.
type ViewAsUI struct {
	Level string `json:"level"` // "" | unlinked | noconfig | linked
	Label string `json:"label"` // display name for the active level
	// RealAdmin is the TRUE admin-mode setting, unaffected by the preview —
	// the one gate the View-as control itself is allowed to use.
	RealAdmin bool `json:"real_admin"`
}

// GetViewAs returns the current preview state.
func (a *App) GetViewAs() ViewAsUI {
	lvl := ViewAsActive()
	return ViewAsUI{Level: lvl, Label: viewAsLabels[lvl], RealAdmin: GetSettings().AdminMode}
}

// SetViewAs enters or exits a preview level. Entering requires real admin
// mode; exiting ("") is always allowed so a preview can never trap you.
func (a *App) SetViewAs(level string) error {
	if level != "" {
		if _, ok := viewAsLabels[level]; !ok {
			return fmt.Errorf("unknown view-as level %q", level)
		}
		if !GetSettings().AdminMode {
			return fmt.Errorf("view-as requires admin mode")
		}
	}

	viewAsMu.Lock()
	if viewAsLevel == level {
		viewAsMu.Unlock()
		return nil
	}
	viewAsLevel = level
	viewAsMu.Unlock()

	// Drop the session-only preview toggles so each entry starts fresh.
	trigStoreMu.Lock()
	viewAsToggles = nil
	trigStoreMu.Unlock()

	if level == "" {
		addStatus("View as: off — back to your own view")
	} else {
		addStatus("View as: %s", viewAsLabels[level])
	}

	// Reassemble the trigger tree (Fuse root presence follows IsLinked; the
	// Personal root swaps for noconfig) and recompile the engine's active set
	// so behavior — not just the UI — matches the persona.
	LoadTriggers()
	return nil
}

// viewAsToggles is the session-only toggle set served while previewing
// "noconfig" — a fresh member's defaults, safely pokeable. Guarded by
// trigStoreMu like the real sets; discarded on every level change.
var viewAsToggles *trigToggleSet

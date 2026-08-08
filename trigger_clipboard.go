package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// Clipboard blocking for the Fuse trigger package.
//
// A trigger with CopyToClipboard set takes the system clipboard every time it
// matches. That's wanted when you're the one pasting the call it prepares, and
// pure disruption when you're not — whatever you had copied is gone, mid-raid,
// with no warning. Members can't edit Fuse triggers, and muting doesn't help:
// mute silences audio only and deliberately leaves the clipboard alone
// (trigger_mutes.go). So this is the missing lever.
//
// Blocks nest exactly like mutes: blocking a group stops every trigger beneath
// it, so one click on the Fuse root stops the whole package from touching the
// clipboard. Effective state = own flag OR any ancestor's. Stored app-wide (not
// per character) — there is one clipboard on the machine, so a per-toon setting
// would be meaningless.
//
// Keyed like the mutes and toggle sets: groups by GroupID, triggers by
// "GroupID/Name" (trigToggleKey), so a block survives a set republish.
// Guarded by trigStoreMu alongside the tree it describes.

var (
	trigClipGroups   = map[int]bool{}
	trigClipTriggers = map[string]bool{}
)

type trigClipsFile struct {
	Groups   map[string]bool `json:"groups"` // GroupID as string (JSON map keys)
	Triggers map[string]bool `json:"triggers"`
}

func trigClipsPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "trigger_clipboard.json")
}

// loadTrigClipsLocked loads the persisted clipboard blocks. Caller holds
// trigStoreMu.
func loadTrigClipsLocked() {
	trigClipGroups = map[int]bool{}
	trigClipTriggers = map[string]bool{}
	data, err := os.ReadFile(trigClipsPath())
	if err != nil {
		return
	}
	var f trigClipsFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for k, v := range f.Groups {
		if id, err := strconv.Atoi(k); err == nil && v {
			trigClipGroups[id] = true
		}
	}
	for k, v := range f.Triggers {
		if v {
			trigClipTriggers[k] = true
		}
	}
}

// saveTrigClipsLocked persists the clipboard blocks. Caller holds trigStoreMu.
func saveTrigClipsLocked() error {
	f := trigClipsFile{Groups: map[string]bool{}, Triggers: map[string]bool{}}
	for id, v := range trigClipGroups {
		if v {
			f.Groups[strconv.Itoa(id)] = true
		}
	}
	for k, v := range trigClipTriggers {
		if v {
			f.Triggers[k] = true
		}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(trigClipsPath(), data)
}

// ── bindings ────────────────────────────────────────────────────────────────

// SetTriggerGroupClipboardBlocked stops (or re-allows) clipboard copying for a
// whole group; subgroups and triggers inherit. The Fuse root covers the entire
// package.
func (a *App) SetTriggerGroupClipboardBlocked(groupID int, blocked bool) error {
	trigStoreMu.Lock()
	if blocked {
		trigClipGroups[groupID] = true
	} else {
		delete(trigClipGroups, groupID)
	}
	err := saveTrigClipsLocked()
	trigStoreMu.Unlock()
	// Recompiles the active set — the block is baked into each compiled trigger.
	RebuildTriggerActivation()
	return err
}

// SetTriggerClipboardBlocked stops (or re-allows) clipboard copying for one
// trigger, keyed by its group + name (triggers have no persistent id of their
// own).
func (a *App) SetTriggerClipboardBlocked(groupID int, name string, blocked bool) error {
	key := strconv.Itoa(groupID) + "/" + name
	trigStoreMu.Lock()
	if blocked {
		trigClipTriggers[key] = true
	} else {
		delete(trigClipTriggers, key)
	}
	err := saveTrigClipsLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// Audio muting for the Fuse trigger package.
//
// Members can't edit Fuse triggers, so when one is too chatty their only lever
// was disabling it — losing the timer bar along with the noise. Muting keeps
// everything the trigger DOES (alerts, timer bars, clipboard) and silences
// only its audio: the match sound, TTS, and the timer's ending/ended sounds.
//
// Mutes nest: muting a group silences every trigger beneath it, so one click
// on the Fuse root mutes the whole package. Effective state = own flag OR any
// ancestor's. Stored app-wide (not per character): whether audio is wanted is
// a speakers-and-ears preference, not a per-toon loadout — unlike the
// enable/disable toggles, which stay per character.
//
// Keyed like the toggle sets: groups by GroupID, triggers by "GroupID/Name"
// (trigToggleKey), so mutes survive a set republish the same way toggles do.
// Guarded by trigStoreMu alongside the tree they describe.

var (
	trigMuteGroups   = map[int]bool{}
	trigMuteTriggers = map[string]bool{}
)

type trigMutesFile struct {
	Groups   map[string]bool `json:"groups"` // GroupID as string (JSON map keys)
	Triggers map[string]bool `json:"triggers"`
}

func trigMutesPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "trigger_mutes.json")
}

// loadTrigMutesLocked loads the persisted mutes. Caller holds trigStoreMu.
func loadTrigMutesLocked() {
	trigMuteGroups = map[int]bool{}
	trigMuteTriggers = map[string]bool{}
	data, err := os.ReadFile(trigMutesPath())
	if err != nil {
		return
	}
	var f trigMutesFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for k, v := range f.Groups {
		if id, err := strconv.Atoi(k); err == nil && v {
			trigMuteGroups[id] = true
		}
	}
	for k, v := range f.Triggers {
		if v {
			trigMuteTriggers[k] = true
		}
	}
}

// saveTrigMutesLocked persists the mutes. Caller holds trigStoreMu.
func saveTrigMutesLocked() error {
	f := trigMutesFile{Groups: map[string]bool{}, Triggers: map[string]bool{}}
	for id, v := range trigMuteGroups {
		if v {
			f.Groups[strconv.Itoa(id)] = true
		}
	}
	for k, v := range trigMuteTriggers {
		if v {
			f.Triggers[k] = true
		}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(trigMutesPath(), data)
}

// groupMutedEffLocked reports whether a group is muted, directly or by any
// ancestor. Caller holds trigStoreMu.
func groupMutedEffLocked(groupID int) bool {
	for cur := groupByID[groupID]; cur != nil; cur = groupParentOf[cur.GroupID] {
		if trigMuteGroups[cur.GroupID] {
			return true
		}
	}
	return false
}

// ── bindings ────────────────────────────────────────────────────────────────

// SetTriggerGroupMuted mutes/unmutes a whole group's audio (subgroups and
// triggers inherit). The Fuse root mutes the entire package.
func (a *App) SetTriggerGroupMuted(groupID int, muted bool) error {
	trigStoreMu.Lock()
	if muted {
		trigMuteGroups[groupID] = true
	} else {
		delete(trigMuteGroups, groupID)
	}
	err := saveTrigMutesLocked()
	trigStoreMu.Unlock()
	// Recompiles the active set so the mute takes effect immediately (it's
	// baked into each compiled trigger and the live-timer mute index).
	RebuildTriggerActivation()
	return err
}

// SetTriggerMuted mutes/unmutes one trigger's audio, keyed by its group + name
// (triggers have no persistent id of their own).
func (a *App) SetTriggerMuted(groupID int, name string, muted bool) error {
	key := strconv.Itoa(groupID) + "/" + name
	trigStoreMu.Lock()
	if muted {
		trigMuteTriggers[key] = true
	} else {
		delete(trigMuteTriggers, key)
	}
	err := saveTrigMutesLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	return err
}

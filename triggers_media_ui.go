package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Wails bindings for trigger audio: the media-file picker used by the edit
// dialog, and the master volume/mute (the speaker control on the Timers tab).

// GetTriggerMediaFiles lists the audio files available to assign to a trigger —
// everything in the local media dir (downloaded from the server + anything the
// user has added), sorted case-insensitively.
func (a *App) GetTriggerMediaFiles() []string {
	entries, err := os.ReadDir(triggerMediaDir())
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		if strings.HasSuffix(low, ".mp3") || strings.HasSuffix(low, ".wav") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// AddTriggerMediaFile opens a file picker, copies the chosen audio file into the
// media dir, and returns its bare name for the form to select. For a Fuse
// trigger the bytes are published to the server when the trigger is saved
// (pushFuseTriggersAsync → SyncTriggerMedia). Returns "" if the user cancels.
func (a *App) AddTriggerMediaFile() (string, error) {
	if v3App == nil {
		return "", fmt.Errorf("unavailable")
	}
	path, err := v3App.Dialog.OpenFile().
		SetTitle("Select an audio file").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("Audio files", "*.mp3;*.wav").
		PromptForSingleSelection()
	if err != nil || strings.TrimSpace(path) == "" {
		return "", err
	}
	base := mediaBasename(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(triggerMediaDir(), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(triggerMediaDir(), base), data, 0600); err != nil {
		return "", err
	}
	return base, nil
}

// PlayTriggerMediaSample plays a media file so the user can hear it while
// editing. Plays even when muted (it's an explicit action), at the master volume.
func (a *App) PlayTriggerMediaSample(name string) {
	playMediaSample(resolveMediaPath(name))
}

// AudioSettingsUI is the master audio state for the speaker control.
type AudioSettingsUI struct {
	Volume int  `json:"volume"` // 0-100
	Muted  bool `json:"muted"`
}

func (a *App) GetAudioSettings() AudioSettingsUI {
	return AudioSettingsUI{Volume: audioVol(), Muted: audioIsMuted()}
}

// SetAudioVolume sets and persists the master volume (0-100).
func (a *App) SetAudioVolume(v int) {
	setAudioVolume(v)
	s := GetSettings()
	s.AudioVolume = audioVol()
	UpdateSettings(s)
}

// SetAudioMuted sets and persists the mute state.
func (a *App) SetAudioMuted(m bool) {
	setAudioMuted(m)
	s := GetSettings()
	s.AudioMuted = m
	UpdateSettings(s)
}

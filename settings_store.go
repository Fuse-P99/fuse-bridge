package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	GuildChat          bool   `json:"guild_chat"`
	GuildMotd          bool   `json:"guild_motd"`
	Broadcasts         bool   `json:"broadcasts"`
	ServerMessages     bool   `json:"server_messages"`
	QuakeMessages      bool   `json:"quake_messages"`
	EngageMessages     bool   `json:"engage_messages"`
	WhoOutput          bool   `json:"who_output"`
	CharacterLocations bool   `json:"character_locations"`
	StartupConfigured  bool   `json:"startup_configured"`
	EQDirectory        string `json:"eq_directory"`
}

var (
	currentSettings Settings
	settingsMu      sync.RWMutex
)

func settingsPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "settings.json")
}

func LoadSettings() Settings {
	defaults := Settings{
		GuildChat:          true,
		GuildMotd:          true,
		Broadcasts:         true,
		ServerMessages:     true,
		QuakeMessages:      true,
		EngageMessages:     true,
		WhoOutput:          true,
		CharacterLocations: true,
	}
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return defaults
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return defaults
	}
	return s
}

func SaveSettings(s Settings) {
	path := settingsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(path, data, 0600)
}

func GetSettings() Settings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return currentSettings
}

func UpdateSettings(s Settings) {
	settingsMu.Lock()
	currentSettings = s
	settingsMu.Unlock()
	SaveSettings(s)
}

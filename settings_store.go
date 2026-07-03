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
	ShareMapPosition   bool   `json:"share_map_position"`
	ExcludeBots        bool   `json:"exclude_bots"`
	ExcludeFiltered    bool   `json:"exclude_filtered"`
	StartupConfigured  bool   `json:"startup_configured"`
	EQDirectory        string `json:"eq_directory"`
	AdminMode          bool   `json:"admin_mode"`
	SlainMessages      bool   `json:"slain_messages"`
	Token              string `json:"token"` // per-client auth token from Discord linking
}

var (
	currentSettings Settings
	settingsMu      sync.RWMutex
)

func settingsPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "settings.json")
}

// defaultSettings returns the baseline settings with every forwarding category
// enabled. Fields not listed here (AdminMode, StartupConfigured, EQDirectory)
// intentionally default to their zero value.
func defaultSettings() Settings {
	return Settings{
		GuildChat:          true,
		GuildMotd:          true,
		Broadcasts:         true,
		ServerMessages:     true,
		QuakeMessages:      true,
		EngageMessages:     true,
		WhoOutput:          true,
		CharacterLocations: true,
		ShareMapPosition:   true,
		SlainMessages:      true,
		ExcludeBots:        true,
		ExcludeFiltered:    true,
	}
}

func LoadSettings() Settings {
	// Start from defaults and unmarshal the saved file ON TOP, so a field that's
	// absent from an older settings.json keeps its default (true) instead of
	// silently becoming false. Booleans explicitly set to false are respected.
	s := defaultSettings()
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return defaultSettings()
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

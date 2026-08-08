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
	BindLocation       bool   `json:"bind_location"` // forward "/char" bind-point lines
	ShareMapPosition   bool   `json:"share_map_position"`
	ExcludeBots        bool   `json:"exclude_bots"`
	ExcludeFiltered    bool   `json:"exclude_filtered"`
	StartupConfigured  bool   `json:"startup_configured"`
	UseMiddlemand      bool   `json:"use_middlemand"` // run the built-in P99 login proxy + manage eqhost.txt
	EQDirectory        string `json:"eq_directory"`
	AdminMode          bool   `json:"dev_mode_fuse_rocks"`
	SlainMessages      bool   `json:"slain_messages"`
	// ResistMessages forwards spell resist/immune lines (any resisted spell, not
	// just procs). ProcMessages forwards weapon proc-effect lines; the server
	// counts a tank's procs from either. Both default on like every other
	// forwarding toggle; the General tab auto-enables Resist when Proc is
	// enabled. See filter.go.
	ResistMessages bool `json:"resist_messages"`
	ProcMessages   bool `json:"proc_messages"`
	// InterruptMessages forwards "Your spell is interrupted." lines so the
	// server can stop the sender's CH cast bar on the raid card. Default on.
	InterruptMessages bool `json:"interrupt_messages"`
	// DisciplineMessages forwards Defensive Discipline lines so the raid card
	// can show the main tank's 3-minute window to everyone, including viewers
	// who aren't in game. Default on. See filter.go.
	DisciplineMessages bool `json:"discipline_messages"`
	// The three below gate what this client contributes to the shared damage
	// and threat picture. Unlike the others in this group they forward no raw
	// log lines — the client aggregates locally and posts numbers — but they
	// belong in the same list because that is where a member looks to see what
	// their client is sending, and each is a category of their play being
	// reported to the server. All default on.
	//
	// MeleeInfo covers damage: the per-mob boards behind Raid DPS and the parse
	// table (raiddps.go), and the damage half of the threat estimate. Off means
	// this client stops contributing damage rows entirely — their name simply
	// won't appear on anyone's meter.
	MeleeInfo bool `json:"melee_info"`
	// SpellInfo covers casting: the spell hate that prices a debuff or a nuke
	// (eq_spells.threat) on the DPS & Threat overlay. Off means a caster's hate
	// reads as zero, which for a pure caster is the whole number.
	SpellInfo bool `json:"spell_info"`
	// PetInfo covers pets identifying themselves in speech, and the owner a pet
	// names when it tells YOU it is following (raiddps.go). Off means this
	// client's pet still counts its damage but is reported under the pet's own
	// name rather than "<You> + Pet".
	PetInfo bool `json:"pet_info"`
	// GameTime forwards parsed "/time" output so the server can aggregate an
	// accurate shared in-game clock (see gametime.go). Default on.
	GameTime bool `json:"game_time"`
	// WorldTimers enables the server-wide Boats & Zone Events board (Scout
	// Charisa, Ring 8, boat schedules) on the Timers tab, and forwards boat
	// dock announcements seen in the log (worldtimers.go). Default on.
	WorldTimers bool   `json:"world_timers"`
	Token       string `json:"token"` // per-client auth token from Discord linking
	// Log archival (Logs tab → Manage Logs). When ArchiveLogs is on, a quiet-period
	// worker moves oversized, non-active eqlog files to ArchiveLogDir and prunes
	// archived files older than ArchiveDeleteDays. See logarchive.go.
	ArchiveLogs       bool   `json:"archive_logs"`
	ArchiveLogDir     string `json:"archive_log_dir"`     // "" → resolved default (Backup/Archive under Logs)
	ArchiveSizeMB     int    `json:"archive_size_mb"`     // threshold; 0 → default 50
	ArchiveDeleteDays int    `json:"archive_delete_days"` // 0 → never delete
	// Trigger audio: master volume (0-100) for TTS + media files, and a mute
	// toggle (speaker button on the Timers tab). AudioVolume defaults to 100.
	AudioVolume int  `json:"audio_volume"`
	AudioMuted  bool `json:"audio_muted"`
	// OverlayTitles controls when overlay title bars show: "" / "always"
	// (default), "locked" (hidden while overlays are locked), or "zero" (shown
	// only while a timer/alert is active). See popouts.go / Popout.svelte.
	OverlayTitles string `json:"overlay_titles"`
	// SnapToGrid snaps overlay move + resize to a 10px grid. See Popout.svelte.
	SnapToGrid bool `json:"snap_to_grid"`
	// HideOverlaysUnfocused hides all overlays while the foreground window
	// belongs to any process other than EverQuest or this app (focuswatch.go).
	// Default off — overlays stay visible over other apps unless opted in.
	HideOverlaysUnfocused bool `json:"hide_overlays_unfocused"`
	// Share identity: the persistent anonymous credential for user-to-user
	// sharing (triggers/markers), independent of Discord linking. ShareSecret
	// authenticates against the server; ShareAddr is the server-assigned public
	// id others send to; ShareName is the toon name last registered under (a
	// mismatch with the current toon triggers a re-register). See sharing.go.
	ShareSecret string `json:"share_secret"`
	ShareAddr   string `json:"share_addr"`
	ShareName   string `json:"share_name"`
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
		BindLocation:       true,
		ShareMapPosition:   true,
		SlainMessages:      true,
		ResistMessages:     true,
		ProcMessages:       true,
		InterruptMessages:  true,
		DisciplineMessages: true,
		MeleeInfo:          true,
		SpellInfo:          true,
		PetInfo:            true,
		ExcludeBots:        true,
		ExcludeFiltered:    true,
		GameTime:           true,
		WorldTimers:        true,
		AudioVolume:        100,
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

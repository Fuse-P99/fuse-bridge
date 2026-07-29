package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tracker mirrors the server's structured tracker.
type Tracker struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Ago  string `json:"ago"`
}

// TimerEntry mirrors the server's timer mob entry.
type TimerEntry struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"` // "popped" | "in_window" | "upcoming"
	Detail    string    `json:"detail"`
	Remaining string    `json:"remaining"`
	Trackers  []Tracker `json:"trackers"`
	IsRaid    bool      `json:"is_raid"`
	Raid      *RaidCard `json:"raid"`
}

// RaidKV mirrors the server's name/value pair. AtMs is when a debuff was last
// cast live (0 = unknown), used for the card's countdown bars.
type RaidKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	AtMs  int64  `json:"at_ms,omitempty"`
}

// RaidCHSlot mirrors a server CH-chain slot. CalledAtMs/InterruptedAtMs feed
// the 10s CH cast bar (0 = unknown / not interrupted).
type RaidCHSlot struct {
	Label           string `json:"label"`
	Cleric          string `json:"cleric"`
	Tank            string `json:"tank"`
	Dead            bool   `json:"dead"`
	CalledAtMs      int64  `json:"called_at_ms,omitempty"`
	InterruptedAtMs int64  `json:"interrupted_at_ms,omitempty"`
}

// RaidLoot mirrors the server's auctioned loot item.
type RaidLoot struct {
	Name    string `json:"name"`
	WikiURL string `json:"wiki_url"`
	Price   string `json:"price"`
}

// RaidRaider mirrors a server raider entry.
type RaidRaider struct {
	Name    string `json:"name"`
	Discord string `json:"discord"`
	Level   int    `json:"level"`
}

// RaidClassGroup mirrors a server class group.
type RaidClassGroup struct {
	Class   string       `json:"class"`
	Members []RaidRaider `json:"members"`
}

// RaidRaiders mirrors the server's class composition.
type RaidRaiders struct {
	Total  int              `json:"total"`
	Groups []RaidClassGroup `json:"groups"`
}

// RaidCurrentTarget mirrors a server non-boss add-mob entry.
type RaidCurrentTarget struct {
	Name    string   `json:"name"`
	Debuffs []RaidKV `json:"debuffs"`
	Sieve   int      `json:"sieve"`
}

// RaidCard mirrors the server's raid detail card.
type RaidCard struct {
	Target           string              `json:"target"`
	Status           string              `json:"status"`
	KilledAgo        string              `json:"killed_ago"`
	TargetHP         int                 `json:"target_hp"`
	ActiveMainTank   string              `json:"active_main_tank"`
	ActiveRampTank   string              `json:"active_ramp_tank"`
	MainTankList     string              `json:"main_tank_list"`
	TrashTankList    string              `json:"trash_tank_list"`
	RampageTankList  string              `json:"rampage_tank_list"`
	BumpList         string              `json:"bump_list"`
	FlufferClerics   string              `json:"fluffer_clerics"`
	Debuffs          []RaidKV            `json:"debuffs"`
	CHChain          []RaidCHSlot        `json:"ch_chain"`
	Loot             []RaidLoot          `json:"loot"`
	Raiders          RaidRaiders         `json:"raiders"`
	DiscordChannelID string              `json:"discord_channel_id"`
	DiscordURL       string              `json:"discord_url"`
	Sieve            int                 `json:"sieve"`
	CurrentTargets   []RaidCurrentTarget `json:"current_targets"`
	CurrentTanks     []string            `json:"current_tanks"`
	TankProcs        map[string]int      `json:"tank_procs"`
	// Zone is the raid target's home zone; the raid special overlays only
	// render for players standing in it ("" = unknown, fail open).
	Zone string `json:"zone"`
}

// BatphoneBanner mirrors the server's freeform batphone banner.
type BatphoneBanner struct {
	Text   string `json:"text"`
	SentAt int64  `json:"sent_at"`
}

// TimersData mirrors the server's parsed timers board.
type TimersData struct {
	Verified       bool             `json:"verified"`
	Porter         string           `json:"porter"`
	Logistics      string           `json:"logistics"`
	Idol           string           `json:"idol"`
	Mobs           []TimerEntry     `json:"mobs"`
	Summary        string           `json:"summary"`
	Updated        string           `json:"updated"`
	FetchedAt      int64            `json:"fetched_at"`
	Batphones      []BatphoneBanner `json:"batphones"`
	CompletedRaids []RaidCard       `json:"completed_raids"`
	// GhostRaid is raid state the server could not attach to a mob — a CH chain
	// running during an event (Sky, Halls of Testing, Ring War) or before a
	// batphone lands. It feeds the three raid special overlays only; it is never
	// a raid on the Raids tab. Its Zone is where the guild actually is, so the
	// overlays' in-zone gate works on it exactly as on a real card.
	GhostRaid *RaidCard `json:"ghost_raid"`
}

// fetchBatphones retrieves current batphone banners (linked members only).
func fetchBatphones() []BatphoneBanner {
	if !IsLinked() {
		return nil
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/batphones", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out struct {
		Batphones []BatphoneBanner `json:"batphones"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Batphones
}

// fetchTimers retrieves the timers board from the server, passing the current
// character so the server can verify Fuse membership.
func fetchTimers(toon string) TimersData {
	var out TimersData
	if !IsLinked() {
		return out
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	u := base + "/timers"
	if toon != "" {
		u += "?toon=" + url.QueryEscape(toon)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return out
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

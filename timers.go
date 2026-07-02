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

// RaidKV mirrors the server's name/value pair.
type RaidKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RaidCHSlot mirrors a server CH-chain slot.
type RaidCHSlot struct {
	Label  string `json:"label"`
	Cleric string `json:"cleric"`
	Tank   string `json:"tank"`
}

// RaidCard mirrors the server's raid detail card.
type RaidCard struct {
	Target          string       `json:"target"`
	Status          string       `json:"status"`
	KilledAgo       string       `json:"killed_ago"`
	ActiveMainTank  string       `json:"active_main_tank"`
	ActiveRampTank  string       `json:"active_ramp_tank"`
	MainTankList    string       `json:"main_tank_list"`
	TrashTankList   string       `json:"trash_tank_list"`
	RampageTankList string       `json:"rampage_tank_list"`
	BumpList        string       `json:"bump_list"`
	FlufferClerics  string       `json:"fluffer_clerics"`
	Debuffs         []RaidKV     `json:"debuffs"`
	CHChain         []RaidCHSlot `json:"ch_chain"`
	Loot            []string     `json:"loot"`
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
	Mobs           []TimerEntry     `json:"mobs"`
	Summary        string           `json:"summary"`
	Updated        string           `json:"updated"`
	FetchedAt      int64            `json:"fetched_at"`
	Batphones      []BatphoneBanner `json:"batphones"`
	CompletedRaids []RaidCard       `json:"completed_raids"`
}

// fetchTimers retrieves the timers board from the server, passing the current
// character so the server can verify Fuse membership.
func fetchTimers(toon string) TimersData {
	var out TimersData
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

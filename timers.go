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
}

// TimersData mirrors the server's parsed timers board.
type TimersData struct {
	Verified  bool         `json:"verified"`
	Porter    string       `json:"porter"`
	Mobs      []TimerEntry `json:"mobs"`
	Summary   string       `json:"summary"`
	Updated   string       `json:"updated"`
	FetchedAt int64        `json:"fetched_at"`
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

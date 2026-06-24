package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	raidMobMu    sync.RWMutex
	raidMobNames = make(map[string]struct{}) // lowercase canonical names + nicknames
)

func raidMobCachePath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "raidmobs.json")
}

// IsRaidMob reports whether name (any case) matches a server-flagged raid mob.
func IsRaidMob(name string) bool {
	raidMobMu.RLock()
	defer raidMobMu.RUnlock()
	_, ok := raidMobNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// LoadRaidMobs loads the cached raid mob list from disk, then asynchronously
// fetches a fresh copy from the server. Call once at startup.
func LoadRaidMobs() {
	loadRaidMobCache()
	go refreshRaidMobs()
}

func loadRaidMobCache() {
	data, err := os.ReadFile(raidMobCachePath())
	if err != nil {
		return
	}
	var payload raidMobsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	applyRaidMobs(payload.Mobs)
}

func refreshRaidMobs() {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/raidmobs", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		addStatus("Raid mob list fetch failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		addStatus("Raid mob list fetch: server returned %d", resp.StatusCode)
		return
	}
	var payload raidMobsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return
	}
	applyRaidMobs(payload.Mobs)
	saveRaidMobCache(payload)
	fmt.Printf("Raid mob list updated: %d mobs\n", len(payload.Mobs))
}

func applyRaidMobs(mobs []string) {
	m := make(map[string]struct{}, len(mobs))
	for _, name := range mobs {
		m[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	raidMobMu.Lock()
	raidMobNames = m
	raidMobMu.Unlock()
}

func saveRaidMobCache(payload raidMobsPayload) {
	path := raidMobCachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

type raidMobsPayload struct {
	Mobs []string `json:"mobs"`
}

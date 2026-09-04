package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BindEntry is a character's last-known bind point (from "/char") and when it
// was seen. Tracked locally per client, exactly like the zone cache.
type BindEntry struct {
	Zone      string    `json:"zone"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	bindMu    sync.RWMutex
	bindCache = make(map[string]BindEntry) // toon name → last bind entry
)

func bindsPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "binds.json")
}

func LoadBinds() {
	data, err := os.ReadFile(bindsPath())
	if err != nil {
		return
	}
	var m map[string]BindEntry
	if json.Unmarshal(data, &m) == nil {
		bindMu.Lock()
		bindCache = m
		bindMu.Unlock()
	}
}

func saveBinds() {
	bindMu.RLock()
	data, err := json.MarshalIndent(bindCache, "", "  ")
	bindMu.RUnlock()
	if err != nil {
		return
	}
	path := bindsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// UpdateLocalBind records a character's current bind point. Called whenever the
// player runs "/char", independent of the forwarding toggle (mirrors zone).
func UpdateLocalBind(toon, zone string) {
	if toon == "" || zone == "" {
		return
	}
	bindMu.Lock()
	bindCache[toon] = BindEntry{Zone: zone, UpdatedAt: time.Now()}
	bindMu.Unlock()
	saveBinds()
}

func GetAllBinds() map[string]BindEntry {
	bindMu.RLock()
	defer bindMu.RUnlock()
	m := make(map[string]BindEntry, len(bindCache))
	for k, v := range bindCache {
		m[k] = v
	}
	return m
}

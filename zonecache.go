package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ZoneEntry struct {
	Zone      string    `json:"zone"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	zoneMu    sync.RWMutex
	zoneCache = make(map[string]ZoneEntry) // toon name → last zone entry
)

func zonesPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "zones.json")
}

func LoadZones() {
	data, err := os.ReadFile(zonesPath())
	if err != nil {
		return
	}
	var m map[string]ZoneEntry
	if json.Unmarshal(data, &m) == nil {
		zoneMu.Lock()
		zoneCache = m
		zoneMu.Unlock()
		return
	}
	// Migrate from old format (map[string]string, no timestamp).
	var old map[string]string
	if json.Unmarshal(data, &old) == nil {
		zoneMu.Lock()
		for k, v := range old {
			zoneCache[k] = ZoneEntry{Zone: v}
		}
		zoneMu.Unlock()
	}
}

func saveZones() {
	zoneMu.RLock()
	data, err := json.MarshalIndent(zoneCache, "", "  ")
	zoneMu.RUnlock()
	if err != nil {
		return
	}
	path := zonesPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

func UpdateLocalZone(toon, zone string) {
	if toon == "" || zone == "" {
		return
	}
	zoneMu.Lock()
	zoneCache[toon] = ZoneEntry{Zone: zone, UpdatedAt: time.Now()}
	zoneMu.Unlock()
	saveZones()
}

func GetAllZones() map[string]ZoneEntry {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	m := make(map[string]ZoneEntry, len(zoneCache))
	for k, v := range zoneCache {
		m[k] = v
	}
	return m
}

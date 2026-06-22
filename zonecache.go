package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var (
	zoneMu    sync.RWMutex
	zoneCache = make(map[string]string) // toon name → last zone
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
	var m map[string]string
	if json.Unmarshal(data, &m) == nil {
		zoneMu.Lock()
		zoneCache = m
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
	zoneCache[toon] = zone
	zoneMu.Unlock()
	saveZones()
}

func GetAllZones() map[string]string {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	copy := make(map[string]string, len(zoneCache))
	for k, v := range zoneCache {
		copy[k] = v
	}
	return copy
}

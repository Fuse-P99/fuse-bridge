package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Persistent local cache of character level/class, keyed by lowercased name and
// stored at %APPDATA%/FuseBridgekeeper/characters.json. Built up from server
// /who data over time so the Characters tab loads instantly and works offline.
var (
	charCacheMu sync.RWMutex
	charCache   = map[string]CharInfo{}
)

func charCachePath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "characters.json")
}

// LoadCharCache reads the cache from disk into memory. Called once at startup.
func LoadCharCache() {
	data, err := os.ReadFile(charCachePath())
	if err != nil {
		return
	}
	var m map[string]CharInfo
	if json.Unmarshal(data, &m) == nil && m != nil {
		charCacheMu.Lock()
		charCache = m
		charCacheMu.Unlock()
	}
}

func saveCharCache() {
	charCacheMu.RLock()
	data, err := json.MarshalIndent(charCache, "", "  ")
	charCacheMu.RUnlock()
	if err != nil {
		return
	}
	path := charCachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// mergeCharInfos merges fresh server data into the cache, never overwriting a
// known level/class with an empty/zero value. Persists if anything changed.
func mergeCharInfos(fresh map[string]CharInfo) {
	if len(fresh) == 0 {
		return
	}
	changed := false
	charCacheMu.Lock()
	for k, v := range fresh {
		cur, ok := charCache[k]
		nv := cur
		if v.Level > 0 {
			nv.Level = v.Level
		}
		if v.Class != "" {
			nv.Class = v.Class
		}
		if !ok || nv != cur {
			charCache[k] = nv
			changed = true
		}
	}
	charCacheMu.Unlock()
	if changed {
		saveCharCache()
	}
}

// cachedCharInfos returns cached entries for the given names (lowercased keys).
func cachedCharInfos(names []string) map[string]CharInfo {
	out := map[string]CharInfo{}
	charCacheMu.RLock()
	defer charCacheMu.RUnlock()
	for _, n := range names {
		if ci, ok := charCache[strings.ToLower(n)]; ok {
			out[strings.ToLower(n)] = ci
		}
	}
	return out
}

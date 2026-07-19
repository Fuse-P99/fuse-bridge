package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Fuse Triggers server sync. The guild's shared trigger set lives on the server;
// officers edit it in the client and those edits write through immediately and
// propagate to everyone. Every user downloads the set when they open the Timers
// window (and at startup); it's cached locally so it works offline.

// ── current character's class (drives class-specific default enablement) ──────

var (
	curClassMu   sync.RWMutex
	curCharClass string

	classTriedMu sync.Mutex
	classTried   = map[string]bool{}
)

// classForCurrentChar returns the resolved class for the tailed character, or ""
// if unknown. Non-blocking (reads a cached value); safe to call under any lock.
func classForCurrentChar() string {
	curClassMu.RLock()
	defer curClassMu.RUnlock()
	return curCharClass
}

func setCurrentCharClass(c string) {
	curClassMu.Lock()
	curCharClass = c
	curClassMu.Unlock()
}

// resolveClassFor updates curCharClass from the local char cache; if the class
// isn't cached it clears it (class-specific stays off) and fetches it once in
// the background, rebuilding activation when it arrives. Call outside trigStoreMu.
func resolveClassFor(charName string) {
	if charName == "" {
		setCurrentCharClass("")
		return
	}
	key := strings.ToLower(charName)
	if ci, ok := cachedCharInfos([]string{charName})[key]; ok && ci.Class != "" {
		setCurrentCharClass(ci.Class)
		return
	}
	setCurrentCharClass("")
	classTriedMu.Lock()
	tried := classTried[key]
	classTried[key] = true
	classTriedMu.Unlock()
	if tried {
		return
	}
	go func() {
		mergeCharInfos(fetchCharInfos([]string{charName}))
		if ci, ok := cachedCharInfos([]string{charName})[key]; ok && ci.Class != "" {
			setCurrentCharClass(ci.Class)
			RebuildTriggerActivation() // recompute now that class-specific can apply
		}
	}()
}

// ── officer status cache (gates Fuse editing on the client) ──────────────────

var (
	officerMu   sync.RWMutex
	officerFlag bool
)

// isOfficerCached reports the last-known officer status (refreshed on sync).
func isOfficerCached() bool {
	officerMu.RLock()
	defer officerMu.RUnlock()
	return officerFlag
}

func refreshOfficerStatus() {
	v := false
	if IsLinked() {
		v = fetchIsOfficer()
	}
	officerMu.Lock()
	officerFlag = v
	officerMu.Unlock()
}

// ── server calls ─────────────────────────────────────────────────────────────

func triggersEndpoint() string {
	return strings.TrimSuffix(serverURL, "/submit") + "/triggers"
}

func fetchFuseTriggers() (version int, payload string, ok bool) {
	if !IsLinked() {
		return 0, "", false
	}
	req, err := http.NewRequest(http.MethodGet, triggersEndpoint(), nil)
	if err != nil {
		return 0, "", false
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", false
	}
	var out struct {
		Version int    `json:"version"`
		Payload string `json:"payload"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return 0, "", false
	}
	return out.Version, out.Payload, true
}

func postFuseTriggers(payload string) (version int, ok bool) {
	body, _ := json.Marshal(map[string]string{"payload": payload})
	req, err := http.NewRequest(http.MethodPost, triggersEndpoint(), bytes.NewReader(body))
	if err != nil {
		return 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var out struct {
		Version int `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Version, true
}

// SyncFuseTriggers downloads the server's Fuse set and adopts it when newer than
// the local cache. If the server is empty and we're an officer with a local set
// (e.g. just migrated from a GINA import), it seeds the server from that set.
// Safe to call from any goroutine; a no-op for non-linked users.
func SyncFuseTriggers() {
	if !IsLinked() {
		return
	}
	refreshOfficerStatus()
	version, payload, ok := fetchFuseTriggers()
	if !ok {
		return
	}

	if version > 0 && strings.TrimSpace(payload) != "" {
		trigStoreMu.Lock()
		changed := false
		if version != fuseVersion || fuseRoot == nil {
			var g GinaGroup
			if xml.Unmarshal([]byte(payload), &g) == nil {
				g.Name = fuseTriggersName
				g.GroupID = fuseRootGroupID
				fuseRoot = &g
				fuseVersion = version
				assembleLocked()
				_ = saveFuseCacheLocked()
				changed = true
			}
		}
		trigStoreMu.Unlock()
		if changed {
			RebuildTriggerActivation()
		}
		return
	}

	// Server empty — seed it from our local set (first officer to open wins).
	if !isOfficerCached() {
		return
	}
	trigStoreMu.Lock()
	var body []byte
	if fuseRoot != nil {
		body, _ = marshalGroupXML(fuseRoot)
	}
	trigStoreMu.Unlock()
	if len(body) == 0 {
		return
	}
	if v, ok := postFuseTriggers(string(body)); ok {
		trigStoreMu.Lock()
		fuseVersion = v
		_ = saveFuseCacheLocked()
		trigStoreMu.Unlock()
		addStatus("Triggers: published your set as Fuse Triggers (v%d)", v)
	}
}

// pushFuseTriggersAsync writes the current Fuse set to the server after an
// officer edit (auto-sync). Fire-and-forget; updates the cached version.
func pushFuseTriggersAsync() {
	go func() {
		trigStoreMu.Lock()
		var body []byte
		if fuseRoot != nil {
			body, _ = marshalGroupXML(fuseRoot)
		}
		trigStoreMu.Unlock()
		if len(body) == 0 {
			return
		}
		if v, ok := postFuseTriggers(string(body)); ok {
			trigStoreMu.Lock()
			fuseVersion = v
			_ = saveFuseCacheLocked()
			trigStoreMu.Unlock()
		}
	}()
}

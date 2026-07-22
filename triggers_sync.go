package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Fuse Triggers server sync. The guild's shared trigger set lives on the server
// as a single versioned copy (v1, v2, …). Every user downloads it when they
// open the Timers window (and at startup); it's cached locally so it works
// offline.
//
// Officer edits are LOCAL until published: each edit marks the local set dirty
// (markFuseDirty) and the sync stops adopting server copies so the work in
// progress is never clobbered. PublishFuseTriggers uploads the local set (the
// server bumps the version and every client picks it up on its next sync);
// RevertFuseTriggers throws the local edits away and re-adopts the server copy.

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
			// A first-time character couldn't inherit a same-class overlay layout
			// while its class was unknown — now it can.
			RetryPopoutClassInheritance(charName)
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

// SyncFuseTriggers downloads the server's Fuse set (adopting it when newer) and
// then reconciles the audio files those triggers reference. Safe to call from
// any goroutine; a no-op for non-linked users.
func SyncFuseTriggers() {
	syncFuseTriggersXML()
	SyncTriggerMedia()
}

// syncFuseTriggersXML downloads the server's Fuse set and adopts it when newer
// than the local cache. If the server is empty and we're an officer with a local
// set (e.g. just migrated from a GINA import), it seeds the server from that set.
func syncFuseTriggersXML() {
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
		fuseServerVersion = version
		// Unpublished officer edits: never overwrite them with the server copy.
		// The newer server version is remembered so the UI can warn that
		// publishing will overwrite someone else's release.
		if fuseDirty {
			trigStoreMu.Unlock()
			return
		}
		changed := false
		if version != fuseVersion || fuseRoot == nil {
			var g GinaGroup
			if xml.Unmarshal([]byte(payload), &g) == nil {
				g.Name = fuseTriggersName
				g.GroupID = fuseRootGroupID
				fuseRoot = &g
				fuseVersion = version
				// Defensively scrub any machine-specific media paths a pre-scrub
				// server payload might still carry down to bare file names.
				scrubMediaNamesInGroup(fuseRoot)
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
		fuseServerVersion = v
		fuseDirty = false
		_ = saveFuseCacheLocked()
		trigStoreMu.Unlock()
		addStatus("Triggers: published your set as Fuse Triggers (v%d)", v)
	}
}

// pushFuseTriggersAsync uploads the current Fuse set in the background. Kept
// only for maintenance publishes (the one-time scrubbed-media republish);
// officer edits go through the explicit publishFuseTriggersNow instead.
// A successful upload leaves the local set in sync (not dirty).
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
			fuseServerVersion = v
			fuseDirty = false
			_ = saveFuseCacheLocked()
			trigStoreMu.Unlock()
			// Publish any audio the edited set newly references.
			SyncTriggerMedia()
		}
	}()
}

// publishFuseTriggersNow uploads the officer's local Fuse set as the new
// published version. Synchronous so the UI can report the new version (or the
// failure). On success every client adopts it on their next sync.
func publishFuseTriggersNow() (int, error) {
	if !IsLinked() {
		return 0, fmt.Errorf("not linked to a Discord account")
	}
	refreshOfficerStatus()
	if !isOfficerCached() {
		return 0, fmt.Errorf("only officers can publish Fuse Triggers")
	}
	trigStoreMu.Lock()
	var body []byte
	if fuseRoot != nil {
		body, _ = marshalGroupXML(fuseRoot)
	}
	trigStoreMu.Unlock()
	if len(body) == 0 {
		return 0, fmt.Errorf("there is no Fuse Triggers set to publish")
	}
	v, ok := postFuseTriggers(string(body))
	if !ok {
		return 0, fmt.Errorf("publish failed — could not reach the server")
	}
	trigStoreMu.Lock()
	fuseVersion = v
	fuseServerVersion = v
	fuseDirty = false
	_ = saveFuseCacheLocked()
	trigStoreMu.Unlock()
	// Upload any audio files the published set newly references.
	go SyncTriggerMedia()
	addStatus("Triggers: published Fuse Triggers v%d", v)
	return v, nil
}

// revertFuseTriggersNow discards local (unpublished) Fuse edits and re-adopts
// the server's current copy.
func revertFuseTriggersNow() error {
	if !IsLinked() {
		return fmt.Errorf("not linked to a Discord account")
	}
	version, payload, ok := fetchFuseTriggers()
	if !ok || version <= 0 || strings.TrimSpace(payload) == "" {
		return fmt.Errorf("could not fetch the server's Fuse Triggers")
	}
	var g GinaGroup
	if xml.Unmarshal([]byte(payload), &g) != nil {
		return fmt.Errorf("the server's Fuse Triggers could not be read")
	}
	g.Name = fuseTriggersName
	g.GroupID = fuseRootGroupID
	trigStoreMu.Lock()
	fuseRoot = &g
	fuseVersion = version
	fuseServerVersion = version
	fuseDirty = false
	scrubMediaNamesInGroup(fuseRoot)
	assembleLocked()
	_ = saveFuseCacheLocked()
	trigStoreMu.Unlock()
	RebuildTriggerActivation()
	addStatus("Triggers: reverted local edits — back on Fuse Triggers v%d", version)
	return nil
}

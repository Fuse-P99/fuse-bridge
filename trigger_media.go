package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// fuseMediaNeedsRepublish is set when localizing scrubbed media paths out of the
// Fuse subtree, so an officer re-pushes the cleaned set to the server (the adopt
// path won't, since the server's dirty copy is at the same version).
var fuseMediaNeedsRepublish atomic.Bool

// Trigger media (the audio files triggers play) is distributed through the
// server alongside the shared trigger set: officers upload the files once, and
// every client downloads the ones its trigger set references into a local media
// directory. Triggers store only the bare file name; playback resolves it here.
//
// This is also where the one-time scrub happens: an imported GINA set carries
// absolute paths like C:\Users\<you>\...\ImportedMediaFiles\X.mp3. On load we
// copy those files into the local media dir and rewrite the stored path down to
// "X.mp3", so no machine-specific path is ever saved or shared.

// triggerMediaDir is where downloaded/localized audio files live (beside
// settings.json, like the other trigger state).
func triggerMediaDir() string {
	return filepath.Join(filepath.Dir(settingsPath()), "media")
}

// mediaBasename reduces a possibly-absolute, either-separator path to its bare
// file name. "" stays "".
func mediaBasename(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `\/`); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func mediaPathExists(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// resolveMediaPath maps a stored media name to its local file, or "" if we don't
// have it yet (not downloaded / never provided). playMedia no-ops on "".
func resolveMediaPath(name string) string {
	name = mediaBasename(name)
	if name == "" {
		return ""
	}
	p := filepath.Join(triggerMediaDir(), name)
	if mediaPathExists(p) {
		return p
	}
	return ""
}

// walkGroupMedia visits every media-name field (the trigger's own plus its
// timer-ending and timer-ended sub-triggers) under the given groups.
func walkGroupMedia(groups []*GinaGroup, fn func(field *string)) {
	var walk func(g *GinaGroup)
	walk = func(g *GinaGroup) {
		for _, t := range g.Triggers {
			fn(&t.MediaFileName)
			if t.TimerEndingTrigger != nil {
				fn(&t.TimerEndingTrigger.MediaFileName)
			}
			if t.TimerEndedTrigger != nil {
				fn(&t.TimerEndedTrigger.MediaFileName)
			}
		}
		for _, c := range g.Groups {
			walk(c)
		}
	}
	for _, g := range groups {
		walk(g)
	}
}

// scrubMediaNamesInGroup rewrites every media path in a subtree down to its bare
// file name (no copying — used when adopting the server's set). Returns whether
// anything changed.
func scrubMediaNamesInGroup(g *GinaGroup) bool {
	if g == nil {
		return false
	}
	changed := false
	walkGroupMedia([]*GinaGroup{g}, func(field *string) {
		if *field == "" {
			return
		}
		if base := mediaBasename(*field); base != *field {
			*field = base
			changed = true
		}
	})
	return changed
}

// localizeAndNormalizeMediaLocked copies any locally-present, absolutely-pathed
// media into the media dir and rewrites every stored media path to its bare name,
// persisting if anything changed. Caller holds trigStoreMu. This is what scrubs
// machine-specific paths out of the saved triggers on first run after import; a
// change in the Fuse subtree flags a republish so the server copy gets cleaned.
func localizeAndNormalizeMediaLocked() {
	fuseChanged, fuseCopied := localizeGroupMediaLocked(fuseRoot)
	personalChanged, _ := localizeGroupMediaLocked(personalRoot)
	if fuseChanged || personalChanged {
		_ = saveTriggersLocked()
	}
	// Only republish the shared set if this client actually holds the media bytes
	// (copied real files) — that's the officer who imported them. Others just
	// scrub locally and download the files from the server.
	if fuseChanged && fuseCopied > 0 {
		fuseMediaNeedsRepublish.Store(true)
	}
}

// localizeGroupMediaLocked localizes + scrubs media paths in one subtree,
// returning whether anything changed and how many real files it copied into the
// media dir. Caller holds trigStoreMu.
func localizeGroupMediaLocked(g *GinaGroup) (changed bool, copied int) {
	if g == nil {
		return false, 0
	}
	walkGroupMedia([]*GinaGroup{g}, func(field *string) {
		raw := strings.TrimSpace(*field)
		if raw == "" {
			return
		}
		base := mediaBasename(raw)
		if base == *field {
			return // already a bare name
		}
		// Had a path: pull the file into our media dir (best effort) before scrubbing
		// the path, so an officer can then publish it to the server.
		if filepath.IsAbs(raw) && copyIntoTriggerMediaDir(raw, base) {
			copied++
		}
		*field = base
		changed = true
	})
	return changed, copied
}

// copyIntoTriggerMediaDir copies src into the media dir as base. Returns true if
// the file is present there afterward (copied now, or already had it). Best
// effort — a missing source is simply skipped.
func copyIntoTriggerMediaDir(src, base string) bool {
	dst := filepath.Join(triggerMediaDir(), base)
	if mediaPathExists(dst) {
		return true
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(triggerMediaDir(), 0700); err != nil {
		return false
	}
	tmp := dst + ".tmp"
	if os.WriteFile(tmp, data, 0600) != nil {
		return false
	}
	return os.Rename(tmp, dst) == nil
}

// referencedMediaNames returns the distinct bare media names the current trigger
// set references.
func referencedMediaNames() []string {
	seen := map[string]bool{}
	var out []string
	trigStoreMu.Lock()
	if trigCfg != nil {
		walkGroupMedia(trigCfg.Groups, func(field *string) {
			n := mediaBasename(*field)
			if n == "" {
				return
			}
			if k := strings.ToLower(n); !seen[k] {
				seen[k] = true
				out = append(out, n)
			}
		})
	}
	trigStoreMu.Unlock()
	return out
}

// ── server transfer ──────────────────────────────────────────────────────────

type triggerMediaMeta struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
	// Library marks a file published from the repo's soundlibrary/ folder —
	// the "Basic Sounds" group in the pickers. Everything else in the shared
	// manifest arrived with the GINA trigger packages.
	Library bool `json:"library"`
}

func triggerMediaEndpoint() string {
	return strings.TrimSuffix(serverURL, "/submit") + "/trigger-media"
}

// fetchTriggerMediaManifest returns the server's media list keyed by lower name.
func fetchTriggerMediaManifest() (map[string]triggerMediaMeta, bool) {
	req, err := http.NewRequest(http.MethodGet, triggerMediaEndpoint(), nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var out struct {
		Files []triggerMediaMeta `json:"files"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil, false
	}
	m := make(map[string]triggerMediaMeta, len(out.Files))
	for _, f := range out.Files {
		m[strings.ToLower(f.Name)] = f
	}
	return m, true
}

func downloadTriggerMediaFile(name string) ([]byte, bool) {
	u := triggerMediaEndpoint() + "?name=" + url.QueryEscape(name)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 24<<20))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func uploadTriggerMediaFile(name, sha string, data []byte) bool {
	body, _ := json.Marshal(map[string]string{
		"name":   name,
		"sha256": sha,
		"data":   base64.StdEncoding.EncodeToString(data),
	})
	req, err := http.NewRequest(http.MethodPost, triggerMediaEndpoint(), bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ── media source cache ──────────────────────────────────────────────────────
//
// The sound pickers group files into "Basic Sounds" (the repo sound library),
// "From Gina" (the rest of the shared manifest — the audio the GINA trigger
// packages brought), and "Personal" (anything local the server doesn't know).
// The split comes from the server manifest, cached here at every media sync so
// opening a picker never blocks on the network. A file in neither list is
// personal by definition.

type mediaSourceCache struct {
	Library []string `json:"library"` // repo soundlibrary names
	Shared  []string `json:"shared"`  // every other published name
}

func mediaSourceCachePath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "media_sources.json")
}

func saveMediaSourceCache(manifest map[string]triggerMediaMeta) {
	var c mediaSourceCache
	for _, m := range manifest {
		if m.Library {
			c.Library = append(c.Library, m.Name)
		} else {
			c.Shared = append(c.Shared, m.Name)
		}
	}
	sort.Slice(c.Library, func(i, j int) bool { return strings.ToLower(c.Library[i]) < strings.ToLower(c.Library[j]) })
	sort.Slice(c.Shared, func(i, j int) bool { return strings.ToLower(c.Shared[i]) < strings.ToLower(c.Shared[j]) })
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(mediaSourceCachePath(), data, 0600)
}

// loadMediaSourceCache returns lowercased name→source ("library"|"gina") for
// every published file, empty when no sync has run yet.
func loadMediaSourceCache() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(mediaSourceCachePath())
	if err != nil {
		return out
	}
	var c mediaSourceCache
	if json.Unmarshal(data, &c) != nil {
		return out
	}
	for _, n := range c.Library {
		out[strings.ToLower(n)] = "library"
	}
	for _, n := range c.Shared {
		out[strings.ToLower(n)] = "gina"
	}
	return out
}

func writeTriggerMediaFile(name string, data []byte) bool {
	if err := os.MkdirAll(triggerMediaDir(), 0700); err != nil {
		return false
	}
	dst := filepath.Join(triggerMediaDir(), mediaBasename(name))
	tmp := dst + ".tmp"
	if os.WriteFile(tmp, data, 0600) != nil {
		return false
	}
	return os.Rename(tmp, dst) == nil
}

var triggerMediaSyncMu sync.Mutex

// SyncTriggerMedia reconciles local media with the server: it downloads any
// referenced files this client is missing, and (officers only) publishes any
// local files the server doesn't have yet. Safe to call from any goroutine; a
// no-op for non-linked users and skipped if a sync is already running.
func SyncTriggerMedia() {
	if !IsLinked() {
		return
	}
	if !triggerMediaSyncMu.TryLock() {
		return
	}
	defer triggerMediaSyncMu.Unlock()

	manifest, ok := fetchTriggerMediaManifest()
	if !ok {
		return
	}

	// Remember which names are shared and which of those are the repo's sound
	// library, so the pickers can group files without a network fetch per open.
	saveMediaSourceCache(manifest)

	// Download referenced files we don't already have locally.
	got := 0
	for _, name := range referencedMediaNames() {
		if mediaPathExists(filepath.Join(triggerMediaDir(), name)) {
			continue
		}
		if _, onServer := manifest[strings.ToLower(name)]; !onServer {
			continue
		}
		if data, ok := downloadTriggerMediaFile(name); ok && writeTriggerMediaFile(name, data) {
			got++
		}
	}
	// Then pull the rest of the shared library. Referenced files above are
	// required for the trigger set to work at all; these are required for the
	// SOUND PICKER to be worth opening — an alarm sound can't be "referenced"
	// before you've picked it, so a download list built only from references can
	// never contain anything new. Officers publish a sound by dropping it in
	// their media folder; this is what makes it selectable for everyone else.
	//
	// Bounded, because it's the one path that fetches files nothing asked for:
	// oversized entries are skipped individually and the pass stops at a total
	// budget, so a stray upload can't turn every client's startup into a
	// download.
	got += downloadSharedMediaLibrary(manifest)
	if got > 0 {
		addStatus("Triggers: downloaded %d media file(s)", got)
	}

	// Officers seed the server from their local media dir (the localize step above
	// populated it from the imported GINA files), then republish the trigger set
	// if scrubbing changed it — the media is on the server first, so no client
	// ever adopts a set referencing files it can't fetch.
	if isOfficerCached() {
		uploadMissingTriggerMedia(manifest)
		if fuseMediaNeedsRepublish.CompareAndSwap(true, false) {
			// Maintenance republish only — never over unpublished officer edits
			// (their eventual Publish carries the scrubbed names anyway).
			trigStoreMu.Lock()
			dirty := fuseDirty
			trigStoreMu.Unlock()
			if !dirty {
				pushFuseTriggersAsync()
				addStatus("Triggers: republished the shared set with scrubbed media paths")
			}
		}
	}
}

const (
	// sharedMediaMaxFile skips a single oversized entry. Alert sounds are a
	// second or two of audio; anything past this is not one.
	sharedMediaMaxFile = 3 << 20 // 3 MB
	// sharedMediaBudget caps one sync pass, so a large library trickles in over
	// several syncs instead of stalling the first one.
	sharedMediaBudget = 20 << 20 // 20 MB
)

// downloadSharedMediaLibrary fetches published media this client doesn't have,
// making every guild sound selectable in the trigger and alarm pickers. Returns
// how many landed.
func downloadSharedMediaLibrary(manifest map[string]triggerMediaMeta) int {
	// Stable order so a library larger than one pass's budget makes the same
	// progress every time rather than re-rolling which files it tries.
	names := make([]string, 0, len(manifest))
	for _, m := range manifest {
		names = append(names, m.Name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })

	got, spent := 0, 0
	for _, name := range names {
		if spent >= sharedMediaBudget {
			break
		}
		m := manifest[strings.ToLower(name)]
		if m.Size > sharedMediaMaxFile {
			continue
		}
		if mediaPathExists(filepath.Join(triggerMediaDir(), name)) {
			continue
		}
		if data, ok := downloadTriggerMediaFile(name); ok && writeTriggerMediaFile(name, data) {
			got++
			spent += len(data)
		}
	}
	return got
}

// fuseReferencedMediaNames returns the media names used by the SHARED Fuse
// trigger set only — not the officer's personal triggers, and not whatever else
// is sitting in their media folder.
func fuseReferencedMediaNames() []string {
	seen := map[string]bool{}
	var out []string
	trigStoreMu.Lock()
	if fuseRoot != nil {
		walkGroupMedia([]*GinaGroup{fuseRoot}, func(field *string) {
			n := mediaBasename(*field)
			if n == "" {
				return
			}
			if k := strings.ToLower(n); !seen[k] {
				seen[k] = true
				out = append(out, n)
			}
		})
	}
	trigStoreMu.Unlock()
	return out
}

// uploadMissingTriggerMedia publishes the audio a shared Fuse trigger set needs,
// so a set an officer publishes works for everyone who receives it.
//
// Scoped to files the FUSE SET references, deliberately. This used to upload the
// officer's whole media folder, which quietly made every sound they had ever
// added to a personal trigger part of the guild's shared library. A player's own
// audio is their own — the way audio becomes shared is by being committed to
// soundlibrary/ in the repo, or by a published set depending on it.
//
// Officer-gated by the caller.
func uploadMissingTriggerMedia(manifest map[string]triggerMediaMeta) {
	sent := 0
	for _, name := range fuseReferencedMediaNames() {
		if strings.HasSuffix(strings.ToLower(name), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(triggerMediaDir(), name))
		if err != nil || len(data) == 0 {
			continue
		}
		sha := sha256Hex(data)
		if m, ok := manifest[strings.ToLower(name)]; ok && strings.EqualFold(m.SHA256, sha) {
			continue // already up to date
		}
		if uploadTriggerMediaFile(name, sha, data) {
			sent++
		}
	}
	if sent > 0 {
		addStatus("Triggers: published %d media file(s) used by the Fuse set", sent)
	}
}

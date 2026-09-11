package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Daily settings snapshot — the one piece of "how is this install configured"
// telemetry the app sends. Once a day, during off hours, the client posts a
// non-personal synopsis of its configuration to the server, which keeps the
// latest one per install and shows officers only aggregates (Clients tab →
// click the one-line dashboard). See PRIVACY.md for the member-facing text.
//
// What goes in (built by buildSettingsSnapshot):
//   - every bool on Settings, by json tag (reflection — a new toggle joins the
//     dataset on its own), plus auto-start; the non-bool preferences by hand
//   - the linked-only server settings (automations, Share 52+ Magelos)
//   - map settings and per-overlay special settings, pushed into Go by the
//     frontend through SetOverlayPrefs (they live in the webview's
//     localStorage otherwise), plus each overlay's open/sticky state
//   - timer/alert overlay category flags (auto-pause, carry, stopwatch, …)
//   - every Fuse trigger: enabled on how many of this install's configured
//     characters, muted, clipboard-blocked, customized — yes/no only
//   - personal triggers (name, group, timer length, overlay), reminders, and
//     the names of custom magelos
//   - whether the install is linked, and a random install id generated once
//
// What never goes in: the EQ directory, tokens, the share secret, any path,
// sound file names, and which CHARACTER a trigger setting belongs to (classes
// only). Anyone adding a setting: read CLAUDE.md "Settings telemetry".
//
// When it goes: a 15-minute ticker asks maybeSendSettingsSnapshot. Due =
// never sent or ≥24h since the last send. It then waits for a quiet moment —
// local 01:00–07:00, or no log activity for an hour (EQ not being played) —
// and gives up waiting once the install is 48h overdue. Like every other
// server push, nothing is sent from a non-home world (auditAllowed).

const (
	snapshotSchema     = 1
	snapshotInterval   = 24 * time.Hour
	snapshotCheckEvery = 15 * time.Minute
	// snapshotForceAfter: due for this long → send at the next check whether
	// or not it's quiet (an install that is never idle still reports).
	snapshotForceAfter = 72 * time.Hour
)

var (
	snapshotProcessStart = time.Now()
	snapshotSendMu       sync.Mutex
)

func snapshotStampPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "snapshot.stamp")
}

// readSnapshotStamp reports when the last snapshot was sent (file mtime).
func readSnapshotStamp() (time.Time, bool) {
	info, err := os.Stat(snapshotStampPath())
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func touchSnapshotStamp() {
	_ = os.WriteFile(snapshotStampPath(), []byte(time.Now().Format(time.RFC3339)), 0600)
}

// startSettingsSnapshotLoop runs the daily-send check every 15 minutes.
func startSettingsSnapshotLoop() {
	go func() {
		for range time.Tick(snapshotCheckEvery) {
			maybeSendSettingsSnapshot()
		}
	}()
}

func snapshotQuietHours() bool {
	h := time.Now().Hour()
	return h >= 1 && h < 7
}

func maybeSendSettingsSnapshot() {
	last, ok := readSnapshotStamp()
	due := !ok || time.Since(last) >= snapshotInterval
	if !due {
		return
	}
	if !ok {
		last = snapshotProcessStart
	}
	forced := time.Since(last) >= snapshotForceAfter
	if !forced && !snapshotQuietHours() && !logIsStale() {
		return // playing right now — wait for a quiet moment
	}
	if !auditAllowed() {
		return
	}
	if err := sendSettingsSnapshot(); err != nil {
		writeLog("settings snapshot: " + err.Error())
	}
}

// sendSettingsSnapshot builds and posts the snapshot. Linked clients send
// their bearer so the server attributes the row; unlinked clients send
// without one and are recorded as unlinked installs.
func sendSettingsSnapshot() error {
	if !snapshotSendMu.TryLock() {
		return fmt.Errorf("a snapshot is already being sent")
	}
	defer snapshotSendMu.Unlock()
	snap := buildSettingsSnapshot()
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, registerBase()+"/clients/settings/snapshot", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FuseBridge/"+clientVersion)
	if h := authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	touchSnapshotStamp()
	addStatus("Settings snapshot sent (%d KB).", (len(body)+1023)/1024)
	writeLog(fmt.Sprintf("settings snapshot sent: %d bytes, linked=%v", len(body), IsLinked()))
	return nil
}

// SendSettingsSnapshot sends one now — the Admin Settings button. Skips the
// schedule, keeps the home-world gate. Bound.
func (a *App) SendSettingsSnapshot() error {
	if !auditAllowed() {
		return fmt.Errorf("not sent — nothing is sent to the server from this world")
	}
	return sendSettingsSnapshot()
}

// ── the payload ─────────────────────────────────────────────────────────────

type snapshotPrefs struct {
	AudioVolume       int    `json:"audio_volume"`
	OverlayTitles     string `json:"overlay_titles"`
	ArchiveSizeMB     int    `json:"archive_size_mb"`
	ArchiveDeleteDays int    `json:"archive_delete_days"`
}

type snapshotMap struct {
	Reported     bool    `json:"reported"`
	Trail        bool    `json:"trail"`
	FocusLevel   bool    `json:"focus_level"`
	Radius       string  `json:"radius"`
	CustomRadius float64 `json:"custom_radius"`
	Grid         string  `json:"grid"`
	Opacity      float64 `json:"opacity"`
	Aot          bool    `json:"aot"`
	Autohide     bool    `json:"autohide"`
	StayUnlocked bool    `json:"stay_unlocked"`
	OverlayOpen  bool    `json:"overlay_open"`
}

type snapshotOverlay struct {
	Kind     string         `json:"kind"`
	Category string         `json:"category"`
	Open     bool           `json:"open"`
	Sticky   bool           `json:"sticky"`
	Settings map[string]any `json:"settings"`
}

type snapshotCategory struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	AutoPause       bool   `json:"auto_pause"`
	CarryTimers     bool   `json:"carry_timers"`
	AlertSeconds    int    `json:"alert_seconds"`
	AlertStopwatch  bool   `json:"alert_stopwatch"`
	FlashEndedEarly bool   `json:"flash_ended_early"`
}

type snapshotTrigger struct {
	K string `json:"k"`
	S string `json:"s"`
	T int    `json:"t"`
	E int    `json:"e"`
	M int    `json:"m"`
	C int    `json:"c"`
	O int    `json:"o"`
}

type snapshotChar struct {
	Class string `json:"class"`
}

type snapshotFuse struct {
	Version  int               `json:"version"`
	Dirty    bool              `json:"dirty"`
	Total    int               `json:"total"`
	Chars    []snapshotChar    `json:"chars"`
	Triggers []snapshotTrigger `json:"triggers"`
}

type snapshotPersonalTrigger struct {
	G    string `json:"g"`
	N    string `json:"n"`
	T    int    `json:"t"`
	Secs int64  `json:"secs"`
	O    string `json:"o"`
}

type snapshotPersonal struct {
	Groups     int                       `json:"groups"`
	Shared     bool                      `json:"shared"`
	GinaGroups int                       `json:"gina_groups"`
	Triggers   []snapshotPersonalTrigger `json:"triggers"`
}

type snapshotReminder struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	LeadMs int64  `json:"lead_ms"`
	Sound  int    `json:"sound"`
	Speak  int    `json:"speak"`
	Repeat int    `json:"repeat"`
}

type snapshotMagelo struct {
	Toon string `json:"toon"`
	Name string `json:"name"`
}

type snapshotPayload struct {
	InstallID      string             `json:"install_id"`
	Schema         int                `json:"schema"`
	ClientVersion  string             `json:"client_version"`
	Linked         bool               `json:"linked"`
	SentMs         int64              `json:"sent_ms"`
	Settings       map[string]bool    `json:"settings"`
	Prefs          snapshotPrefs      `json:"prefs"`
	LinkedSettings map[string]bool    `json:"linked_settings,omitempty"`
	Map            snapshotMap        `json:"map"`
	Overlays       []snapshotOverlay  `json:"overlays"`
	Categories     []snapshotCategory `json:"categories"`
	Fuse           snapshotFuse       `json:"fuse"`
	Personal       snapshotPersonal   `json:"personal"`
	Reminders      []snapshotReminder `json:"reminders"`
	// Magelos is a pointer so "linked, none" ([]) and "not reported" (absent)
	// stay distinguishable to the aggregate.
	Magelos *[]snapshotMagelo `json:"magelos,omitempty"`
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// settingsBoolMap is every bool on Settings by json tag — the same reflection
// walk as auditSettingsDiff, so a new toggle joins the snapshot without
// anyone remembering to add it. startup_configured is internal; guild_motd is
// a dead toggle (it controls nothing — see GeneralTab.svelte).
func settingsBoolMap(s Settings) map[string]bool {
	out := map[string]bool{}
	v := reflect.ValueOf(s)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type.Kind() != reflect.Bool {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" || tag == "startup_configured" || tag == "guild_motd" {
			continue
		}
		out[tag] = v.Field(i).Bool()
	}
	out["auto_start"] = isAutoStartEnabled()
	return out
}

func buildSettingsSnapshot() snapshotPayload {
	s := GetSettings()
	linked := IsLinked()
	p := snapshotPayload{
		InstallID:     s.InstallID,
		Schema:        snapshotSchema,
		ClientVersion: clientVersion,
		Linked:        linked,
		SentMs:        time.Now().UnixMilli(),
		Settings:      settingsBoolMap(s),
		Prefs: snapshotPrefs{
			AudioVolume:       s.AudioVolume,
			OverlayTitles:     s.OverlayTitles,
			ArchiveSizeMB:     s.ArchiveSizeMB,
			ArchiveDeleteDays: s.ArchiveDeleteDays,
		},
		Overlays:   []snapshotOverlay{},
		Categories: collectCategorySnapshot(),
		Reminders:  []snapshotReminder{},
	}
	if p.Prefs.OverlayTitles == "" {
		p.Prefs.OverlayTitles = "always"
	}

	// Linked-only server settings — omitted entirely when either read fails,
	// so the aggregate never counts a guessed value.
	if linked {
		if auto, err := fetchAutomations(); err == nil {
			if share, err2 := wailsApp.GetMageloAutoShare(); err2 == nil {
				p.LinkedSettings = map[string]bool{
					"automation_add_tracking": auto.AddTracking,
					"automation_swap_bot":     auto.SwapBot,
					"automation_add_missed":   auto.AddMissed,
					"share_magelos":           share,
				}
			}
		}
	}

	prefs := readOverlayPrefs()
	p.Map = collectMapSnapshot(prefs)
	p.Overlays = collectOverlaySnapshot(prefs)
	p.Fuse, p.Personal = collectTriggerSnapshot()

	for _, al := range GetWorldAlarms() {
		p.Reminders = append(p.Reminders, snapshotReminder{
			Key:    al.Key,
			Label:  al.Label,
			LeadMs: al.LeadMs,
			Sound:  boolInt(strings.TrimSpace(al.Sound) != ""),
			Speak:  boolInt(al.Speak),
			Repeat: boolInt(al.Repeat),
		})
	}

	if linked {
		var out struct {
			Magelos []snapshotMagelo `json:"magelos"`
		}
		if err := mageloPost("/magelo/mine", map[string]any{}, &out); err == nil {
			if out.Magelos == nil {
				out.Magelos = []snapshotMagelo{}
			}
			p.Magelos = &out.Magelos
		}
	}
	return p
}

// ── map + overlay preferences (pushed in by the frontend) ───────────────────

var overlayPrefsMu sync.Mutex

func overlayPrefsPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "overlay_prefs.json")
}

func readOverlayPrefsLocked() map[string]map[string]any {
	out := map[string]map[string]any{}
	data, err := os.ReadFile(overlayPrefsPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	if out == nil {
		out = map[string]map[string]any{}
	}
	return out
}

func readOverlayPrefs() map[string]map[string]any {
	overlayPrefsMu.Lock()
	defer overlayPrefsMu.Unlock()
	return readOverlayPrefsLocked()
}

// overlayPrefKey is "kind|category", suffixed "@char" for a per-character
// entry. The suffix never leaves this file: the snapshot folds an overlay's
// entries together (collectOverlaySnapshot) and carries no names.
func overlayPrefKey(kind, category, char string) string {
	k := kind + "|" + category
	if char = strings.TrimSpace(char); char != "" {
		k += "@" + char
	}
	return k
}

func splitOverlayPrefKey(key string) (kind, category, char string) {
	if i := strings.LastIndex(key, "@"); i >= 0 {
		char, key = key[i+1:], key[:i]
	}
	kind, category, _ = strings.Cut(key, "|")
	return kind, category, char
}

// SetOverlayPrefs records a webview's display preferences for one overlay so
// the daily snapshot can read them from Go — they otherwise live only in the
// browser store. MapTab pushes its own settings under kind "maptab";
// Popout.svelte pushes each overlay's gear-panel settings under its kind (and
// category for timer/alert overlays); Manage Overlays pushes what it edits;
// and the main window pushes every stored entry at startup
// (lib/overlayPrefsSync.js), so nothing waits on an overlay being opened.
//
// char is the character the entry belongs to ("" for app-wide entries like
// the map). Trigger and special overlay settings are stored per character,
// and keying the mirror by overlay alone let whichever character's overlay
// mounted last overwrite the rest — an Audio Cue set on the cleric read as
// off once an alt's overlay pushed its defaults. Keys MERGE into what's
// stored, so callers can push disjoint subsets. Bound.
func (a *App) SetOverlayPrefs(kind, category, prefsJSON, char string) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	category = strings.TrimSpace(category)
	if kind == "" {
		return fmt.Errorf("kind is required")
	}
	if len(prefsJSON) > 8<<10 {
		return fmt.Errorf("prefs too large")
	}
	var in map[string]any
	if json.Unmarshal([]byte(prefsJSON), &in) != nil || in == nil {
		return fmt.Errorf("prefs must be a JSON object")
	}
	overlayPrefsMu.Lock()
	defer overlayPrefsMu.Unlock()
	all := readOverlayPrefsLocked()
	key := overlayPrefKey(kind, category, char)
	cur := all[key]
	if cur == nil {
		cur = map[string]any{}
	}
	for k, v := range in {
		cur[k] = v
	}
	all[key] = cur
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(overlayPrefsPath(), data)
}

func prefBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func prefFloat(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return def
}

func prefString(m map[string]any, key string, def string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func collectMapSnapshot(prefs map[string]map[string]any) snapshotMap {
	tab, haveTab := prefs["maptab|"]
	look, haveLook := prefs["map|"]
	m := snapshotMap{
		Reported:     haveTab || haveLook,
		Trail:        prefBool(tab, "trail", false),
		FocusLevel:   prefBool(tab, "focus_level", false),
		Radius:       prefString(tab, "radius", ""),
		CustomRadius: prefFloat(tab, "custom_radius", 0),
		Grid:         prefString(tab, "grid", "off"),
		Opacity:      prefFloat(look, "opacity", 0.85),
		Aot:          prefBool(look, "aot", true),
		Autohide:     prefBool(look, "autohide", true),
	}
	if len(m.Radius) > 32 {
		m.Radius = m.Radius[:32]
	}
	popoutsMu.Lock()
	m.StayUnlocked = !mapLocksWithAllLocked()
	m.OverlayOpen = popoutMapSt != nil && popoutMapSt.Open
	popoutsMu.Unlock()
	return m
}

// overlayPrefKeys is the whitelist of gear-panel settings that travel in the
// snapshot. Colors and file names never do; a chosen cue sound becomes
// has_pulsesound.
var overlayPrefKeys = []string{"opacity", "aot", "autohide", "fit", "flash", "timing", "pulseaudio",
	"smarthide", "breakdown", "sides", "avg", "speed", "offset", "speedo", "focusmode"}

func sanitizeOverlayPrefs(p map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range overlayPrefKeys {
		v, ok := p[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case bool, float64:
			out[k] = x
		case string:
			if len(x) > 32 {
				x = x[:32]
			}
			out[k] = x
		}
	}
	if s, ok := p["pulsesound"].(string); ok {
		out["has_pulsesound"] = strings.TrimSpace(s) != ""
	}
	return out
}

// collectOverlaySnapshot folds the stored entries into one per overlay. The
// entries are per character (see SetOverlayPrefs); what the snapshot asks is
// whether this INSTALL uses a setting, so a yes/no is on if any character has
// it on, and a number or choice comes from the active character's entry, else
// the app-wide one, else the first by key. Character names stay behind.
func collectOverlaySnapshot(prefs map[string]map[string]any) []snapshotOverlay {
	popoutsMu.Lock()
	active := popoutActiveChar
	popoutsMu.Unlock()

	byKey := map[string]*snapshotOverlay{}
	get := func(kind, category string) *snapshotOverlay {
		k := kind + "|" + category
		if e := byKey[k]; e != nil {
			return e
		}
		e := &snapshotOverlay{Kind: kind, Category: category, Settings: map[string]any{}}
		byKey[k] = e
		return e
	}
	keys := make([]string, 0, len(prefs))
	for key := range prefs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Which entry an overlay's scalars came from so far: 2 = the active
	// character's, 1 = app-wide, 0 = another character's.
	scalarRank := map[string]int{}
	for _, key := range keys {
		kind, category, char := splitOverlayPrefKey(key)
		if kind == "" || kind == "maptab" {
			continue
		}
		rank := 0
		if char == "" {
			rank = 1
		} else if strings.EqualFold(char, active) {
			rank = 2
		}
		e := get(kind, category)
		k := kind + "|" + category
		best, seen := scalarRank[k]
		takeScalars := !seen || rank >= best
		for name, v := range sanitizeOverlayPrefs(prefs[key]) {
			if b, ok := v.(bool); ok {
				prev, _ := e.Settings[name].(bool)
				e.Settings[name] = prev || b
				continue
			}
			if _, have := e.Settings[name]; takeScalars || !have {
				e.Settings[name] = v
			}
		}
		if takeScalars {
			scalarRank[k] = rank
		}
	}
	popoutsMu.Lock()
	if popoutMapSt != nil {
		e := get("map", "")
		e.Open, e.Sticky = popoutMapSt.Open, popoutMapSt.Sticky
	}
	for _, st := range popoutChars[popoutActiveChar] {
		if st == nil || st.Kind == "" {
			continue
		}
		e := get(st.Kind, st.Category)
		e.Open = e.Open || st.Open
		e.Sticky = e.Sticky || st.Sticky
	}
	popoutsMu.Unlock()

	out := make([]snapshotOverlay, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Category < out[j].Category
	})
	return out
}

func collectCategorySnapshot() []snapshotCategory {
	catStyleMu.Lock()
	out := make([]snapshotCategory, 0, len(catStyles))
	for _, s := range catStyles {
		if s == nil {
			continue
		}
		out = append(out, snapshotCategory{
			Name:            s.Name,
			Kind:            s.Kind,
			AutoPause:       s.AutoPause,
			CarryTimers:     s.CarryTimers,
			AlertSeconds:    s.AlertSeconds,
			AlertStopwatch:  s.AlertStopwatch,
			FlashEndedEarly: s.FlashEndedEarly,
		})
	}
	catStyleMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ── triggers ────────────────────────────────────────────────────────────────

// collectTriggerSnapshot flattens the Fuse package (enabled across this
// install's configured characters, mutes, clipboard blocks, customizations —
// all yes/no) and lists the Personal set. One walk under trigStoreMu using
// the same effective-state helpers the engine and the tree use.
func collectTriggerSnapshot() (snapshotFuse, snapshotPersonal) {
	fuse := snapshotFuse{Chars: []snapshotChar{}, Triggers: []snapshotTrigger{}}
	personal := snapshotPersonal{Triggers: []snapshotPersonalTrigger{}, GinaGroups: readGinaImportStamp()}

	// Characters first: GetTriggerCharacters takes trigStoreMu itself.
	var keys []string
	for _, c := range wailsApp.GetTriggerCharacters() {
		if c.Key == "" || strings.EqualFold(c.Key, trigDefaultsChar) || !(c.Configured || c.Current) {
			continue
		}
		keys = append(keys, c.Key)
		fuse.Chars = append(fuse.Chars, snapshotChar{Class: c.Class})
	}

	trigStoreMu.Lock()
	defer trigStoreMu.Unlock()

	ctxs := make([]trigEnableCtx, 0, len(keys))
	for _, k := range keys {
		ctxs = append(ctxs, trigCtxForLocked(k, false))
	}

	if fuseRoot != nil && IsLinked() {
		fuse.Version, fuse.Dirty = fuseVersion, fuseDirty
		var walk func(g *GinaGroup, section string, path []string)
		walk = func(g *GinaGroup, section string, path []string) {
			for _, t := range g.Triggers {
				key := trigToggleKey(g, t)
				e := 0
				for _, ctx := range ctxs {
					if effectiveTriggerEnabledLocked(g, t, ctx) {
						e++
					}
				}
				fuse.Triggers = append(fuse.Triggers, snapshotTrigger{
					K: strings.Join(append(append([]string{}, path...), t.Name), "/"),
					S: section,
					T: boolInt(t.TimerType == "Timer"),
					E: e,
					M: boolInt(groupMutedEffLocked(g.GroupID) || trigMuteTriggers[key]),
					C: boolInt(groupClipEffLocked(g.GroupID) || trigClipTriggers[key]),
					O: boolInt(!trigOverrides[key].empty()),
				})
			}
			for _, sub := range g.Groups {
				walk(sub, section, append(append([]string{}, path...), sub.Name))
			}
		}
		for _, sec := range fuseRoot.Groups {
			walk(sec, sec.Name, []string{sec.Name})
		}
		fuse.Total = len(fuse.Triggers)
	}

	if personalRoot != nil {
		for _, sub := range personalRoot.Groups {
			if strings.EqualFold(strings.TrimSpace(sub.Name), sharedGroupName) {
				personal.Shared = true
			}
		}
		var walk func(g *GinaGroup, path []string)
		walk = func(g *GinaGroup, path []string) {
			for _, t := range g.Triggers {
				secs := t.TimerMillisecondDuration / 1000
				if secs <= 0 {
					secs = int64(t.TimerDuration)
				}
				personal.Triggers = append(personal.Triggers, snapshotPersonalTrigger{
					G:    strings.Join(path, " › "),
					N:    t.Name,
					T:    boolInt(t.TimerType == "Timer"),
					Secs: secs,
					O:    effCategoryLocked(g, t),
				})
			}
			for _, sub := range g.Groups {
				personal.Groups++
				walk(sub, append(append([]string{}, path...), sub.Name))
			}
		}
		walk(personalRoot, nil)
	}
	return fuse, personal
}

// ── GINA import stamp ───────────────────────────────────────────────────────

// Imported GINA groups are indistinguishable from app-created ones once in
// the Personal set (ids are reassigned, Modified is stamped by edits too), so
// ImportGINAGroups keeps a running count in a stamp file for the snapshot.
func ginaImportStampPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "gina_imports.stamp")
}

func readGinaImportStamp() int {
	data, err := os.ReadFile(ginaImportStampPath())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func bumpGinaImportStamp(n int) {
	if n <= 0 {
		return
	}
	total := readGinaImportStamp() + n
	_ = os.WriteFile(ginaImportStampPath(), []byte(strconv.Itoa(total)), 0600)
}

// ── install id ──────────────────────────────────────────────────────────────

// newInstallID is 16 random bytes as hex — the snapshot's per-install key.
// It identifies an install, never a person, and is generated once.
func newInstallID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a time-derived id rather than no id at all.
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

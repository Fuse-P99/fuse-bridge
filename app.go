package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed frontend/dist
var assets embed.FS

var (
	wailsApp    *App
	v3App       *application.App           // set by runWails before Run()
	mainWindow  *application.WebviewWindow // the single settings/UI window
	wailsReady  = make(chan struct{})      // closed once the app's main loop is up
	wailsFailed = make(chan struct{})      // closed if app.Run returns an error
)

type App struct{}

var logPath = filepath.Join(os.TempDir(), "FuseBridge.log")
var badWordFilter = false

func writeLog(msg string) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05.000"), msg)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line)
}

func NewApp() *App { return &App{} }

var (
	upgradingMu   sync.Mutex
	upgradingFlag bool
)

// IsUpgrading reports whether a startup upgrade is in progress, so the frontend
// can show the "Upgrading…" screen instead of the normal UI.
func (a *App) IsUpgrading() bool {
	upgradingMu.Lock()
	defer upgradingMu.Unlock()
	return upgradingFlag
}

// BeginUpgrade flips the app into the upgrading state, tells the frontend, and
// brings the window forward so the user understands why it's about to restart.
func (a *App) BeginUpgrade(newVersion string) {
	upgradingMu.Lock()
	upgradingFlag = true
	upgradingMu.Unlock()
	if v3App != nil {
		v3App.Event.Emit("upgrading", newVersion)
	}
	a.Show()
}

// EndUpgrade backs out of the upgrading state after a failed update attempt so
// the user isn't stranded on the "Upgrading…" screen; the frontend returns to
// the normal UI and the failure itself lands in the activity feed.
func (a *App) EndUpgrade() {
	upgradingMu.Lock()
	upgradingFlag = false
	upgradingMu.Unlock()
	if v3App != nil {
		v3App.Event.Emit("upgrade-failed")
	}
}

// Show brings the Wails window to the foreground. Safe to call from any goroutine
// (v3 window methods dispatch to the main thread internally).
func (a *App) Show() {
	if mainWindow == nil {
		return
	}
	// Deliberately not centered: the window belongs wherever the user last put
	// it. This only steps in when that spot is no longer on any monitor.
	ensureMainWindowOnScreen()
	mainWindow.Show()
	// Brief always-on-top flicker ensures the window comes to front even if
	// another app is currently focused.
	mainWindow.SetAlwaysOnTop(true)
	mainWindow.SetAlwaysOnTop(false)
}

// runWails builds the v3 application (bindings, window, tray) and runs its main
// loop. Blocks until Quit. Must be called from main(): the application package
// locks the main OS thread in its init — WebView2's COM apartment and the
// systray message loop are thread-affine.
func runWails() {
	writeLog("runWails() called")
	v3App = application.New(application.Options{
		Name:        "Fuse Bridge",
		Description: "Fuse guild chat bridge client for Project 1999.",
		Services: []application.Service{
			application.NewService(wailsApp),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			// Closing (hiding) the window must not exit — the tray keeps the app alive.
			DisableQuitOnLastWindowClosed: true,
		},
	})

	// Reopen where the user left off. Until the window has been placed once,
	// HasPos is false and the OS centers it as before (see windowstate.go).
	geom := MainWindowGeom()
	initPos := application.WindowCentered
	if geom.HasPos {
		initPos = application.WindowXY
	}

	mainWindow = v3App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Fuse Bridge",
		Width:            geom.W,
		Height:           geom.H,
		InitialPosition:  initPos,
		X:                geom.X,
		Y:                geom.Y,
		MinWidth:         mainWinMinW,
		MinHeight:        mainWinMinH,
		Hidden:           true,
		BackgroundColour: application.NewRGB(15, 17, 23),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	})
	trackMainWindow(mainWindow)

	// Hide instead of quit — quitting happens via the tray menu.
	mainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		// Closing is the moment the user most expects the position to stick, so
		// don't leave it to the debounce.
		captureMainWindowGeom()
		FlushMainWindowGeom()
		mainWindow.Hide()
	})

	setupTray(v3App)

	v3App.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Screens can only be enumerated now, so the off-screen rescue and the
		// maximised restore both happen here rather than at creation. Before
		// wailsReady is signalled: that's what releases the goroutine that may
		// call Show(), and a half-applied restore is what it would race with.
		applyMainWindowState()
		select {
		case <-wailsReady:
		default:
			writeLog("ApplicationStarted — wailsReady closed")
			close(wailsReady)
		}
		// Restore no overlays at startup — neither the app-wide map nor the
		// per-character timer overlays. The tailer names the most recently played
		// toon even on a restart while EQ is closed, so opening anything now would
		// splash overlays over the desktop. Always defer — maybeApplyDeferredPopouts
		// opens the map and this toon's timers once a fresh log line is written
		// after launch (i.e. the toon is actually being played now).
		startupPopoutsPending.Store(true)
		if currentCharName != "" {
			addStatus("Overlays deferred until %s's log is active", currentCharName)
		}
	})

	if err := v3App.Run(); err != nil {
		writeLog("app.Run error: " + err.Error())
		addStatus("UI failed to start: %v", err)
		close(wailsFailed)
	} else {
		writeLog("app.Run returned nil (normal shutdown)")
	}
}

// --- Status ---

type StatusSnapshot struct {
	EQRunning  bool     `json:"eq_running"`
	Configured bool     `json:"configured"`
	LogFile    string   `json:"log_file"`
	Connected  bool     `json:"connected"`
	Activity   []string `json:"activity"`
	Version    string   `json:"version"`
}

func (a *App) GetStatus() StatusSnapshot {
	eq, lf, conn, lines := getStatusSnapshot()
	rev := make([]string, len(lines))
	for i, l := range lines {
		rev[len(lines)-1-i] = l
	}
	return StatusSnapshot{
		EQRunning:  eq,
		Configured: lf != "" || eqLogsPresent(GetSettings().EQDirectory),
		LogFile:    lf,
		Connected:  conn,
		Activity:   rev,
		Version:    clientVersion,
	}
}

// eqLogsPresent reports whether dir is a valid EQ install with at least one
// log file — i.e. we've found the correct folder and identified EQ logs.
func eqLogsPresent(dir string) bool {
	for _, logsDir := range logsDirCandidates(dir) {
		matches, _ := filepath.Glob(filepath.Join(logsDir, "eqlog_*.txt"))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

// --- Settings ---

func (a *App) GetSettings() Settings { return GetSettings() }

func (a *App) SaveSettings(s Settings) {
	cur := GetSettings()
	s.StartupConfigured = cur.StartupConfigured
	UpdateSettings(s)
	// React to the "Use middlemand" toggle (start/stop proxy + eqhost.txt).
	if s.UseMiddlemand != cur.UseMiddlemand {
		go SetMiddlemandEnabled(s.UseMiddlemand)
	}
}

// GetAvailableUpdate returns the newer client version the server offers, or
// "" when up to date — the Status tab shows an Update button when non-empty.
func (a *App) GetAvailableUpdate() string {
	if _, v, ok := availableUpdate(); ok {
		return v
	}
	return ""
}

// StartUpdate applies an available update immediately (user-initiated, so the
// auto-update quiet-hours gating doesn't apply). Shows the upgrade screen,
// then downloads and swaps the binary via the standard update flow.
func (a *App) StartUpdate() {
	base, newVer, ok := availableUpdate()
	if !ok {
		return
	}
	go func() {
		a.BeginUpgrade(newVer)
		time.Sleep(2 * time.Second) // let the upgrade screen render
		if err := applyUpdate(base); err != nil {
			addStatus("Update failed: %v", err)
			writeLog("manual update failed: " + err.Error())
			a.EndUpgrade()
		}
	}()
}

func (a *App) GetAutoStart() bool { return isAutoStartEnabled() }

func (a *App) SetAutoStart(enabled bool) error { return setAutoStart(enabled) }

func (a *App) BrowseEQDirectory() string {
	if v3App == nil {
		return ""
	}
	dir, err := v3App.Dialog.OpenFile().
		SetTitle("Select your EverQuest installation folder").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil || dir == "" {
		return ""
	}
	// Accept any folder that looks like an EQ install (eqgame.exe present or an
	// existing Logs folder, incl. the VirtualStore redirect). Fresh installs
	// have no Logs folder yet — EQ creates it on the first log line — so
	// requiring it rejected perfectly valid directories.
	if !looksLikeEQDir(dir) {
		return "INVALID"
	}
	// Pre-create Logs so tailing starts the moment EQ writes its first line.
	// Best-effort: under Program Files this can fail without admin, in which
	// case the game's writes land in VirtualStore, which we also watch.
	os.MkdirAll(filepath.Join(dir, "Logs"), 0755)
	cur := GetSettings()
	cur.EQDirectory = dir
	UpdateSettings(cur)
	return dir
}

// --- Characters ---

type CharEntry struct {
	Name       string `json:"name"`
	MatchCount int    `json:"match_count"`
	IsBot      bool   `json:"is_bot"`
	IsFiltered bool   `json:"is_filtered"`
}

func (a *App) GetCharNames(query string, excludeBots, excludeFiltered bool) []CharEntry {
	// Keep the bot-filter list current (throttled). Heals a failed/stale startup
	// fetch so the "Exclude Bots" filter works without an app restart.
	maybeRefreshBotToons()

	eqDir := GetSettings().EQDirectory
	allNames := getAllCharNames(eqDir)
	lowerQ := strings.ToLower(strings.TrimSpace(query))

	var out []CharEntry
	for _, n := range allNames {
		isBot := IsBotToon(n)
		isFiltered := IsFilteredToon(n)
		if excludeBots && isBot {
			continue
		}
		if excludeFiltered && isFiltered {
			continue
		}
		if lowerQ == "" {
			out = append(out, CharEntry{Name: n, IsBot: isBot, IsFiltered: isFiltered})
			continue
		}
		content := buildCharContent(n, eqDir)
		count := len(allMatches(n, lowerQ)) + len(allMatches(content, lowerQ))
		if count > 0 {
			out = append(out, CharEntry{Name: n, MatchCount: count, IsBot: isBot, IsFiltered: isFiltered})
		}
	}
	return out
}

func (a *App) GetCharContent(name string) string {
	return buildCharContent(name, GetSettings().EQDirectory)
}

// GetCharInfos returns cached level/class for the given names instantly (from the
// local %APPDATA% cache; works offline). Use RefreshCharInfos to pull fresh data.
func (a *App) GetCharInfos(names []string) map[string]CharInfo {
	return cachedCharInfos(names)
}

// RefreshCharInfos pulls fresh level/class from the server, merges it into the
// local cache (persisting it), and returns the updated values for names. Falls
// back to whatever is cached if the server is unreachable.
func (a *App) RefreshCharInfos(names []string) map[string]CharInfo {
	mergeCharInfos(fetchCharInfos(names))
	return cachedCharInfos(names)
}

type InventoryItem struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	// ItemID is the game's own item id, which the dump carries and nothing
	// else does — the wiki almost never publishes it. It is the only way to
	// tell apart items sharing a name, and there are real cases: the Veeshan's
	// Peak key quest has nine separate items all called "Piece of a Medallion".
	// Reported back to the server for names it has no id for; see
	// ReportItemIDs. 0 means the column was absent or unparseable.
	ItemID int `json:"item_id"`
	Count  int `json:"count"`
}

func (a *App) GetCharInventory(name string) []InventoryItem {
	return readInventoryItems(name, GetSettings().EQDirectory)
}

// readInventoryItems parses CHARNAME-Inventory.txt into items. Returns nil if the
// file is missing. Shared by GetCharInventory and the Characters table view.
func readInventoryItems(name, eqDir string) []InventoryItem {
	path := eqRootFilePath(eqDir, name+"-Inventory.txt")
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var items []InventoryItem
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || i == 0 { // skip header row
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		itemName := parts[1]
		if itemName == "" || itemName == "Empty" {
			continue
		}
		// Columns are Location, Name, ID, Count, Slots.
		itemID := 0
		if len(parts) > 2 {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil && n > 0 {
				itemID = n
			}
		}
		count := 1
		if len(parts) > 3 {
			if n, err := strconv.Atoi(parts[3]); err == nil && n > 0 {
				count = n
			}
		}
		items = append(items, InventoryItem{
			Location: parts[0],
			Name:     itemName,
			ItemID:   itemID,
			Count:    count,
		})
	}
	return items
}

// normalizeItemName lowercases and treats backtick as apostrophe so item names
// from the inventory file match the target names (EQ writes ` for possessives).
func normalizeItemName(n string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(n), "`", "'"))
}

// keyColumns maps each key-check column ID to the exact inventory item that
// proves flagged access. Order defines the table's column order.
var keyColumns = []struct {
	ID   string
	Item string
}{
	{"cs", "Tooth of the Cobalt Scar"}, // Cobalt Scar
	{"ss", "Shrine Key"},               // Sleeper's Tomb shrine / Skyshrine
	{"hs", "Key to Charasis"},          // Howling Stones / Charasis
	{"seb", "Trakanon Idol"},           // Sebilis
	{"st", "Sleeper's Key"},            // Sleeper's Tomb
	{"vp", "Key of Veeshan"},           // Veeshan's Peak
}

// itemColumns maps the Characters table's Utilities / Mobilization /
// Consumables column IDs to the exact inventory item that fills the cell.
// Counted (not boolean): a character can carry several of these, and the
// table shows the count when it's more than one.
var itemColumns = []struct {
	ID   string
	Item string
}{
	// Utilities
	{"rp", "Reaper of the Dead"},
	{"jb", "Journeyman's Boots"},
	{"peg", "Pegasus Feather Cloak"},
	{"tbw", "Thin Boned Wand"},
	{"ttm", "Forlorn Totem of Rolfron Zek"},
	{"sof", "Scepter of the Forlorn"},
	// Mobilization
	{"wc", "Leatherfoot Raider Skullcap"},
	{"ot", "Worker Sledgemallet"},
	{"thg", "Vial of Velium Vapors"},
	// Consumables
	{"sow", "10 Dose Blood of the Wolf"},
	{"shr", "10 Dose Ant's Potion"},
	{"wrt", "10 Dose Potion of Stinging Wort"},
	{"nul", "10 Dose Greater Null Potion"},
}

// CharTableRow is one character's row in the Characters table view.
type CharTableRow struct {
	Name        string          `json:"name"`
	Level       int             `json:"level"`
	Class       string          `json:"class"`
	Race        string          `json:"race"`
	Zone        string          `json:"zone"`
	ZoneUpdated int64           `json:"zone_updated"` // unix millis, 0 = unknown
	Bind        string          `json:"bind"`
	BindUpdated int64           `json:"bind_updated"`
	Keys        map[string]bool `json:"keys"`  // keyColumns ID -> has item
	Items       map[string]int  `json:"items"` // itemColumns ID -> count carried
}

// GetCharTable assembles the Characters table: per-character level/class/race
// (refreshed from the server), last-seen zone and bind point (local caches, with
// update timestamps), and key-item flags read from each inventory file.
func (a *App) GetCharTable(excludeBots, excludeFiltered bool) []CharTableRow {
	maybeRefreshBotToons()
	eqDir := GetSettings().EQDirectory

	var names []string
	for _, n := range getAllCharNames(eqDir) {
		if excludeBots && IsBotToon(n) {
			continue
		}
		if excludeFiltered && IsFilteredToon(n) {
			continue
		}
		names = append(names, n)
	}

	// Pull fresh level/class/race/zone from the server into the local cache.
	mergeCharInfos(fetchCharInfos(names))
	infos := cachedCharInfos(names)

	// Zone/bind caches are keyed by exact toon name; index by lowercase to match.
	zoneByLower := make(map[string]ZoneEntry)
	for toon, ze := range GetAllZones() {
		zoneByLower[strings.ToLower(toon)] = ze
	}
	bindByLower := make(map[string]BindEntry)
	for toon, be := range GetAllBinds() {
		bindByLower[strings.ToLower(toon)] = be
	}

	rows := make([]CharTableRow, 0, len(names))
	for _, n := range names {
		k := strings.ToLower(n)
		ci := infos[k]
		row := CharTableRow{
			Name:  n,
			Level: ci.Level,
			Class: ci.Class,
			Race:  ci.Race,
			Zone:  ci.Zone, // cachedCharInfos already overlays the fresh local zone
			Keys:  make(map[string]bool, len(keyColumns)),
		}
		if ze, ok := zoneByLower[k]; ok {
			if ze.Zone != "" {
				row.Zone = ze.Zone
			}
			if !ze.UpdatedAt.IsZero() {
				row.ZoneUpdated = ze.UpdatedAt.UnixMilli()
			}
		}
		if be, ok := bindByLower[k]; ok {
			row.Bind = be.Zone
			if !be.UpdatedAt.IsZero() {
				row.BindUpdated = be.UpdatedAt.UnixMilli()
			}
		}
		itemCount := make(map[string]int)
		for _, it := range readInventoryItems(n, eqDir) {
			c := it.Count
			if c < 1 {
				c = 1
			}
			itemCount[normalizeItemName(it.Name)] += c
		}
		for _, kc := range keyColumns {
			row.Keys[kc.ID] = itemCount[normalizeItemName(kc.Item)] > 0
		}
		row.Items = make(map[string]int, len(itemColumns))
		for _, ic := range itemColumns {
			row.Items[ic.ID] = itemCount[normalizeItemName(ic.Item)]
		}
		rows = append(rows, row)
	}
	return rows
}

func (a *App) IsFilteredToon(name string) bool { return IsFilteredToon(name) }

func (a *App) ToggleFilteredToon(name string) { ToggleFilteredToon(name) }

// SetFilteredToons batch-filters or -unfilters toons (Characters tab multi-select).
func (a *App) SetFilteredToons(names []string, filtered bool) { SetFilteredToons(names, filtered) }

func (a *App) IsBotToon(name string) bool { return IsBotToon(name) }

// GetCharSpellbook reads CHARNAME-Spellbook.txt (written by /outputfile spellbook)
// and returns the spell names it contains. Returns nil if the file doesn't exist.
// VirtualStore-aware: freshest copy wins (see eqRootFilePath).
func (a *App) GetCharSpellbook(name string) []string {
	path := eqRootFilePath(GetSettings().EQDirectory, name+"-Spellbook.txt")
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var names []string
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		if name == "" {
			continue
		}
		// Detect a header row by CONTENT, not position. EQ's /outputfile
		// spellbook has no header, so the old unconditional skip of line 0
		// silently dropped whatever spell sat in the first slot — for every
		// character. A real data row's first column is the slot number.
		if strings.EqualFold(strings.TrimSpace(parts[0]), "slot") || strings.EqualFold(name, "name") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// Get bad word filter setting
func (a *App) GetIniSettings() []string {
	var inisettings []string
	path := eqRootFilePath(GetSettings().EQDirectory, "eqclient.ini")
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	for _, line := range lines {
		inisettings = append(inisettings, line)
	}
	return inisettings
}

// SpellEntry mirrors SpellResult from the server's /spells endpoint.
type SpellEntry struct {
	Name        string `json:"name"`
	Level       int    `json:"level"`
	Mana        int    `json:"mana"`
	CastTime    string `json:"cast_time"`
	WikiURL     string `json:"wiki_url"`
	Description string `json:"description"`
	SpellType   string `json:"spell_type"`
}

// GetCharClassWithInference determines a character's class using two steps:
//  1. Server lookup — checks the guild roster and whotracker DB.
//  2. Spellbook inference — if spellNames are provided and step 1 fails, the
//     server finds which class most exclusively owns those spells.
//
// spellNames should be the output of GetCharSpellbook. Pass nil or empty to
// skip inference. Returns "" if class cannot be determined.
func (a *App) GetCharClassWithInference(name string, spellNames []string) string {
	base := strings.TrimSuffix(serverURL, "/submit")
	client := &http.Client{Timeout: 8 * time.Second}

	// Step 1: server roster + whotracker lookup.
	if resp, err := client.Get(base + "/charclass?name=" + url.QueryEscape(name)); err == nil {
		var result struct {
			Class string `json:"class"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if result.Class != "" {
			return result.Class
		}
	}

	// Step 2: infer from class-exclusive spells in the spellbook.
	if len(spellNames) == 0 {
		return ""
	}
	body, _ := json.Marshal(map[string][]string{"spells": spellNames})
	req, err := http.NewRequest(http.MethodPost, base+"/inferclass", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := client.Do(req); err == nil {
		var result struct {
			Class string `json:"class"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		return result.Class
	}
	return ""
}

// GetSpellsForClass fetches all spells for a class from the server,
// ordered by level ascending (the UI reverses this for display).
func (a *App) GetSpellsForClass(class string) []SpellEntry {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet,
		base+"/spells?class="+url.QueryEscape(class), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Spells []SpellEntry `json:"spells"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Spells
}

// --- Map ---

// GetPlayerPosition returns the local player's most recent /loc reading.
func (a *App) GetPlayerPosition() PlayerPosition { return GetPosition() }

// GetCurrentZone returns the zone the local player is currently in.
func (a *App) GetCurrentZone() string { return CurrentZone() }

// GetGameClock returns the resolved in-game clock anchor for the footer (server
// fleet-aggregated when available, else this client's local estimate).
func (a *App) GetGameClock() GameClockInfo { return GetGameClock() }

// GetWorldTimers returns the server-wide Events/Boats board for the Timers
// tab (briefly cached; Enabled=false when the toggle is off or unlinked).
func (a *App) GetWorldTimers() WorldTimersData { return fetchWorldTimers() }

// GetTimers returns the raid timers board (gated server-side to Fuse members;
// the current character is sent for verification). It also refreshes the live
// HP filter's set of popped mobs to watch.
func (a *App) GetTimers() TimersData {
	data := fetchTimers(currentCharName)
	var popped []string
	for _, m := range data.Mobs {
		if m.Status == "popped" {
			popped = append(popped, m.Name)
			if m.Raid != nil && m.Raid.Target != "" {
				popped = append(popped, m.Raid.Target)
			}
		}
	}
	SetWatchedMobs(popped)
	return data
}

// GetMobHPs returns live per-mob health percents (lower name → 0-100) parsed from
// the local log, for responsive health bars. Polled quickly by the Raids tab.
func (a *App) GetMobHPs() map[string]int {
	return GetMobHPs()
}

// GetLocalRaidTimers returns locally-parsed debuff and CH-chain cast timers
// (plus the Fuse package's configured debuff durations) for the raid card's
// timer bars. Polled by the Raids tab and the raid section overlays.
func (a *App) GetLocalRaidTimers() LocalRaidTimers {
	return GetLocalRaidTimersData()
}

// GetBatphones returns current batphone banners for the app-wide alert bar
// (linked members only, gated server-side).
func (a *App) GetBatphones() []BatphoneBanner {
	return fetchBatphones()
}

// --- Account linking ---

// IsLinked reports whether this client has completed Discord account linking.
func (a *App) IsLinked() bool { return IsLinked() }

// StartLinking requests a fresh link code to display to the user.
func (a *App) StartLinking() (string, error) { return StartLinking() }

// PollLinking checks whether the code has been linked; saves the token on success.
func (a *App) PollLinking(code string) (bool, error) { return PollLinking(code) }

// Unlink revokes and clears this client's token so it can re-link (admin reset).
func (a *App) Unlink() error { return Unlink() }

// GetCharacterName returns the local player's current character name.
func (a *App) GetCharacterName() string { return currentCharName }

// GetGuildMapPositions returns live positions of guild members in the given zone.
func (a *App) GetGuildMapPositions(zone string) []MapPosition {
	positions, err := fetchMapPositions(zone)
	if err != nil {
		return nil
	}
	return positions
}

// StartMapStrobe flashes the player's live map position on every client's map
// for 10 seconds. Officer-only; the server enforces the role (403 otherwise).
func (a *App) StartMapStrobe() error {
	return sendMapStrobe(currentCharName)
}

// GetAutomations returns the linked member's raid-log automation settings plus
// their rostered toons (main-character dropdown source). Zero value when the
// client isn't linked or the server is unreachable.
func (a *App) GetAutomations() AutomationSettings {
	s, err := fetchAutomations()
	if err != nil {
		return AutomationSettings{}
	}
	return s
}

// SetAutomations stores the automation settings. The tracking/swap toggles off
// clears the main toon (also enforced server-side); addMissed is independent.
func (a *App) SetAutomations(addTracking, swapBot, addMissed bool, mainToon string) error {
	return saveAutomations(addTracking, swapBot, addMissed, mainToon)
}

// GetZoneInfo returns every zone's long name + nicknames, for resolving a zone
// display name to a bundled map file base.
func (a *App) GetZoneInfo() []ZoneNick {
	zones, err := fetchZoneInfo()
	if err != nil {
		return nil
	}
	return zones
}

// --- Zones ---

// wailsZoneData mirrors zoneData with LastSeen as Unix milliseconds so the
// Wails binding generator (which can't handle time.Time) accepts the type.
type wailsZoneData struct {
	Name       string     `json:"name"`
	LastSeen   int64      `json:"last_seen"`
	Characters []zoneChar `json:"characters"`
}

func (a *App) GetZones() ([]wailsZoneData, error) {
	zones, err := fetchZoneSnoop()
	if err != nil {
		return nil, err
	}
	out := make([]wailsZoneData, len(zones))
	for i, z := range zones {
		out[i] = wailsZoneData{
			Name:       z.Name,
			LastSeen:   z.LastSeen.UnixMilli(),
			Characters: z.Characters,
		}
	}
	return out, nil
}

// GetToonIdentities returns a map of toon name (lowercased) → Discord identity,
// for labeling Fuse members in the Zones tab.
func (a *App) GetToonIdentities() map[string]string {
	identities, err := fetchToonIdentities()
	if err != nil {
		return map[string]string{}
	}
	return identities
}

// --- Clients (admin) ---

// wailsClientEntry mirrors adminClientEntry with LastSeen as Unix milliseconds.
type wailsClientEntry struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Toon     string `json:"toon"`
	Guild    string `json:"guild"`
	LastZone string `json:"last_zone"`
	Version  string `json:"version"`
	LastSeen int64  `json:"last_seen"`
	Status   string `json:"status"` // "active" | "connected" | "offline"
	Muted    bool   `json:"muted"`
}

// SetClientMuted toggles server-side muting of a client (admin Clients tab).
func (a *App) SetClientMuted(id int, muted bool) error {
	return muteClient(id, muted)
}

// ScrapeSpellPreview scrapes a wiki spell URL and returns the parsed fields for
// admin review (nothing is written). Admin "Add missing spell" flow.
func (a *App) ScrapeSpellPreview(url string) (SpellPayload, error) {
	return scrapeSpellPreview(url)
}

// AddSpell writes a reviewed spell to the server DB. Admin "Add missing spell".
func (a *App) AddSpell(p SpellPayload) error {
	return addSpell(p)
}

func (a *App) IsAdminMode() bool { return GetSettings().AdminMode }

// ── server-timer alarms (see worldalarms.go) ────────────────────────────────

// GetWorldAlarms returns this player's configured board reminders.
func (a *App) GetWorldAlarms() []WorldAlarm { return GetWorldAlarms() }

// SetWorldAlarm creates or replaces one. Errors are shown to the user, so they
// explain what to fix rather than what failed.
func (a *App) SetWorldAlarm(al WorldAlarm) error { return SetWorldAlarm(al) }

// DeleteWorldAlarm removes the alarm on a board entry.
func (a *App) DeleteWorldAlarm(key string) { DeleteWorldAlarm(key) }

// TestWorldAlarm plays an alarm as configured, without saving it.
func (a *App) TestWorldAlarm(al WorldAlarm) { TestWorldAlarm(al) }

// ── boat trip recorder (admin calibration; see boattrack.go) ────────────────

// StartBoatTrack begins recording zone transitions and marker-phrase sightings
// with their log timestamps, for measuring a boat loop by riding it.
func (a *App) StartBoatTrack(marker string) { StartBoatTrack(marker) }

// StopBoatTrack pauses recording; the events stay readable.
func (a *App) StopBoatTrack() { StopBoatTrack() }

// ClearBoatTrack discards the recording.
func (a *App) ClearBoatTrack() { ClearBoatTrack() }

// GetBoatTrack returns the current recording for the admin UI.
func (a *App) GetBoatTrack() BoatTrackData { return GetBoatTrackData() }

// IsOfficer reports whether the linked member holds the guild's officer role
// (config officer_role_id). Used only to also reveal the Clients tab to
// officers — all other admin_mode features remain admin-only.
func (a *App) IsOfficer() bool { return fetchIsOfficer() }

// GetClientActivity returns the server's rolling feed of relay-client actions.
func (a *App) GetClientActivity() []string {
	lines, err := fetchClientActivity()
	if err != nil {
		return []string{}
	}
	return lines
}

func (a *App) GetClients() ([]wailsClientEntry, error) {
	clients, err := fetchClients()
	if err != nil {
		return nil, err
	}
	out := make([]wailsClientEntry, len(clients))
	for i, c := range clients {
		out[i] = wailsClientEntry{
			ID:       c.ID,
			Name:     c.Name,
			Toon:     c.Toon,
			Guild:    c.Guild,
			LastZone: c.LastZone,
			Version:  c.Version,
			LastSeen: c.LastSeen.UnixMilli(),
			Status:   c.Status,
			Muted:    c.Muted,
		}
	}
	return out, nil
}

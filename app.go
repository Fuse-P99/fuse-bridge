package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend/dist
var assets embed.FS

var (
	wailsApp      *App
	wailsReady    = make(chan struct{})
	wailsFailed   = make(chan struct{}) // closed if wails.Run returns an error
)

type App struct {
	ctx context.Context
}

var logPath = filepath.Join(os.TempDir(), "FuseBridge.log")

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

func (a *App) startup(ctx context.Context) {
	writeLog("startup() called")
	a.ctx = ctx
	close(wailsReady)
	writeLog("wailsReady closed")
}

// Show brings the Wails window to the foreground. Safe to call from any goroutine.
func (a *App) Show() {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowCenter(a.ctx)
	wailsruntime.WindowShow(a.ctx)
	// Brief always-on-top flicker ensures the window comes to front even if
	// another app is currently focused.
	wailsruntime.WindowSetAlwaysOnTop(a.ctx, true)
	wailsruntime.WindowSetAlwaysOnTop(a.ctx, false)
}

func startWails() {
	// Pin this goroutine to its OS thread for the lifetime of the Wails run.
	// WebView2 uses COM STA, which is thread-affine. Without this, Go's scheduler
	// can migrate the goroutine to a different OS thread mid-loop, breaking the
	// COM apartment and causing RunMainLoop() to exit spuriously.
	runtime.LockOSThread()
	writeLog("startWails() called")
	err := wails.Run(&options.App{
		Title:     "Fuse Bridge",
		Width:     900,
		Height:    650,
		MinWidth:  700,
		MinHeight: 500,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 255},
		OnStartup:        wailsApp.startup,
		Bind:             []interface{}{wailsApp},
		StartHidden:      true,
		OnBeforeClose: func(ctx context.Context) bool {
			// Hide instead of quit — the tray keeps the app alive.
			wailsruntime.WindowHide(ctx)
			return true
		},
		Windows: &windows.Options{
			Theme: windows.Dark,
		},
	})
	if err != nil {
		writeLog("wails.Run error: " + err.Error())
		addStatus("UI failed to start: %v", err)
		close(wailsFailed)
	} else {
		writeLog("wails.Run returned nil (normal shutdown)")
	}
}

// --- Status ---

type StatusSnapshot struct {
	EQRunning bool     `json:"eq_running"`
	LogFile   string   `json:"log_file"`
	Connected bool     `json:"connected"`
	Activity  []string `json:"activity"`
	Version   string   `json:"version"`
}

func (a *App) GetStatus() StatusSnapshot {
	eq, lf, conn, lines := getStatusSnapshot()
	rev := make([]string, len(lines))
	for i, l := range lines {
		rev[len(lines)-1-i] = l
	}
	return StatusSnapshot{
		EQRunning: eq,
		LogFile:   lf,
		Connected: conn,
		Activity:  rev,
		Version:   clientVersion,
	}
}

// --- Settings ---

func (a *App) GetSettings() Settings { return GetSettings() }

func (a *App) SaveSettings(s Settings) {
	cur := GetSettings()
	s.StartupConfigured = cur.StartupConfigured
	UpdateSettings(s)
}

func (a *App) GetAutoStart() bool { return isAutoStartEnabled() }

func (a *App) SetAutoStart(enabled bool) error { return setAutoStart(enabled) }

func (a *App) BrowseEQDirectory() string {
	dir, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select your EverQuest installation folder",
	})
	if err != nil || dir == "" {
		return ""
	}
	if _, statErr := os.Stat(filepath.Join(dir, "Logs")); statErr != nil {
		return "INVALID"
	}
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

type InventoryItem struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
}

func (a *App) GetCharInventory(name string) []InventoryItem {
	eqDir := GetSettings().EQDirectory
	if eqDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(eqDir, name+"-Inventory.txt"))
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
		count := 1
		if len(parts) > 3 {
			if n, err := strconv.Atoi(parts[3]); err == nil && n > 0 {
				count = n
			}
		}
		items = append(items, InventoryItem{
			Location: parts[0],
			Name:     itemName,
			Count:    count,
		})
	}
	return items
}

func (a *App) IsFilteredToon(name string) bool { return IsFilteredToon(name) }

func (a *App) ToggleFilteredToon(name string) { ToggleFilteredToon(name) }

func (a *App) IsBotToon(name string) bool { return IsBotToon(name) }

// GetCharSpellbook reads CHARNAME-Spellbook.txt (written by /outputfile spellbook)
// and returns the spell names it contains. Returns nil if the file doesn't exist.
func (a *App) GetCharSpellbook(name string) []string {
	eqDir := GetSettings().EQDirectory
	if eqDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(eqDir, name+"-Spellbook.txt"))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var names []string
	for i, line := range lines {
		if i == 0 {
			continue // header row: Slot\tName\tID
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			continue
		}
		names = append(names, strings.TrimSpace(parts[1]))
	}
	return names
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
		var result struct{ Class string `json:"class"` }
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
		var result struct{ Class string `json:"class"` }
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
	req.Header.Set("Authorization", "Bearer "+apiKey)
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

// --- Clients (admin) ---

// wailsClientEntry mirrors adminClientEntry with LastSeen as Unix milliseconds.
type wailsClientEntry struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	LastSeen  int64  `json:"last_seen"`
	Connected bool   `json:"connected"`
}

func (a *App) IsAdminMode() bool { return GetSettings().AdminMode }

func (a *App) GetClients() ([]wailsClientEntry, error) {
	clients, err := fetchClients()
	if err != nil {
		return nil, err
	}
	out := make([]wailsClientEntry, len(clients))
	for i, c := range clients {
		out[i] = wailsClientEntry{
			Name:      c.Name,
			Version:   c.Version,
			LastSeen:  c.LastSeen.UnixMilli(),
			Connected: c.Connected,
		}
	}
	return out, nil
}

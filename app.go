package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend/dist
var assets embed.FS

var (
	wailsApp   *App
	wailsReady = make(chan struct{})
)

type App struct {
	ctx context.Context
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	close(wailsReady)
}

// Show brings the Wails window to the foreground. Safe to call from any goroutine.
func (a *App) Show() {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	// Brief always-on-top flicker ensures the window comes to front even if
	// another app is currently focused.
	runtime.WindowSetAlwaysOnTop(a.ctx, true)
	runtime.WindowSetAlwaysOnTop(a.ctx, false)
}

func startWails() {
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
			runtime.WindowHide(ctx)
			return true
		},
		Windows: &windows.Options{
			Theme: windows.Dark,
		},
	})
	if err != nil {
		fmt.Println("Wails error:", err)
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
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
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

func (a *App) GetCharNames(excludeBots, excludeFiltered bool) []string {
	eqDir := GetSettings().EQDirectory
	allNames := getAllCharNames(eqDir)
	if !excludeBots && !excludeFiltered {
		return allNames
	}
	var out []string
	for _, n := range allNames {
		if excludeBots && IsBotToon(n) {
			continue
		}
		if excludeFiltered && IsFilteredToon(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (a *App) GetCharContent(name string) string {
	return buildCharContent(name, GetSettings().EQDirectory)
}

func (a *App) IsFilteredToon(name string) bool { return IsFilteredToon(name) }

func (a *App) ToggleFilteredToon(name string) { ToggleFilteredToon(name) }

func (a *App) IsBotToon(name string) bool { return IsBotToon(name) }

// --- Zones ---

func (a *App) GetZones() ([]zoneData, error) { return fetchZoneSnoop() }

// --- Clients (admin) ---

func (a *App) IsAdminMode() bool { return GetSettings().AdminMode }

func (a *App) GetClients() ([]adminClientEntry, error) { return fetchClients() }

package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func (a *App) GetCharNames(query string, excludeBots, excludeFiltered bool) []string {
	eqDir := GetSettings().EQDirectory
	allNames := getAllCharNames(eqDir)
	lowerQ := strings.ToLower(strings.TrimSpace(query))

	var out []string
	for _, n := range allNames {
		if excludeBots && IsBotToon(n) {
			continue
		}
		if excludeFiltered && IsFilteredToon(n) {
			continue
		}
		if lowerQ == "" {
			out = append(out, n)
			continue
		}
		// Name match is fast; content match is slower but thorough.
		if strings.Contains(strings.ToLower(n), lowerQ) {
			out = append(out, n)
			continue
		}
		if strings.Contains(strings.ToLower(buildCharContent(n, eqDir)), lowerQ) {
			out = append(out, n)
		}
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

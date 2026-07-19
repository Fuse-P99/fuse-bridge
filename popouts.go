package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Overlay popout windows: the map and one window per timer category, popped out
// of the main UI as frameless, transparent, always-on-top game overlays.
//
// The set of open overlays and their geometry persist app-wide (not per
// character) in popouts.json, so relaunching the app reopens every overlay that
// was open, at the position and size it last had. Per-overlay look settings
// (background colour/opacity, always-on-top) live in the frontend's
// localStorage, which WebView2 keeps across restarts for the same origin.
var (
	popoutsMu     sync.Mutex
	popouts       = map[string]*application.WebviewWindow{} // live windows by name
	popoutSt      = map[string]*popoutState{}               // persisted state by name
	popoutsLocked bool                                      // Lock toggle, persisted; guarded by popoutsMu
	appQuitting   bool                                      // guarded by popoutsMu
)

// MarkAppQuitting records that the app is intentionally shutting down, so the
// overlay close hooks don't mark their windows closed — a graceful quit must
// keep the open set intact so those overlays reopen on the next launch.
func MarkAppQuitting() {
	popoutsMu.Lock()
	appQuitting = true
	popoutsMu.Unlock()
}

// Overlay visibility: overlays hide together when the player camps out or the
// log goes quiet (auto), and via the footer Hide/Restore Windows button
// (manual). The two are OR'd — restoring one doesn't un-hide the other.
var (
	popoutVisMu         sync.Mutex
	popoutsManualHidden bool // Timers-window Hide toggle
	popoutsAutoHidden   bool // camp-out / long idle
)

func popoutsEffectiveHidden() bool {
	popoutVisMu.Lock()
	defer popoutVisMu.Unlock()
	return popoutsManualHidden || popoutsAutoHidden
}

// applyPopoutVisibility shows or hides every open overlay to match the effective
// hidden state. The main window is never touched.
func applyPopoutVisibility() {
	hide := popoutsEffectiveHidden()
	popoutsMu.Lock()
	wins := make([]*application.WebviewWindow, 0, len(popouts))
	for _, w := range popouts {
		wins = append(wins, w)
	}
	popoutsMu.Unlock()
	for _, w := range wins {
		if hide {
			w.Hide()
		} else {
			w.Show()
		}
	}
}

// SetPopoutsHidden is the footer Hide/Restore Windows toggle (manual).
func (a *App) SetPopoutsHidden(hidden bool) {
	popoutVisMu.Lock()
	popoutsManualHidden = hidden
	popoutVisMu.Unlock()
	applyPopoutVisibility()
}

// ArePopoutsHidden reports the effective hidden state — the map overlay's own
// auto-hide consults this so it doesn't fight the global hide.
func (a *App) ArePopoutsHidden() bool { return popoutsEffectiveHidden() }

// ArePopoutsManuallyHidden / ArePopoutsLocked report the manual toggle states so
// the Timers-window buttons show the right label after a tab remount.
func (a *App) ArePopoutsManuallyHidden() bool {
	popoutVisMu.Lock()
	defer popoutVisMu.Unlock()
	return popoutsManualHidden
}
func (a *App) ArePopoutsLocked() bool {
	popoutsMu.Lock()
	defer popoutsMu.Unlock()
	return popoutsLocked
}

// setPopoutsAutoHidden latches the camp-out/idle auto-hide (true) and clears it
// when log activity resumes (false). Called from the trigger engine. Idempotent.
func setPopoutsAutoHidden(hidden bool) {
	popoutVisMu.Lock()
	if popoutsAutoHidden == hidden {
		popoutVisMu.Unlock()
		return
	}
	popoutsAutoHidden = hidden
	popoutVisMu.Unlock()
	applyPopoutVisibility()
}

// SetAllPopoutsLocked locks/unlocks every overlay at once (the Timers-window
// button). Locking makes them click-through and non-movable; unlocking is the
// escape hatch for an overlay that can't unlock itself. It also notifies the
// overlays so they re-enable their drag bar / resize grip.
func (a *App) SetAllPopoutsLocked(locked bool) {
	mapName, _, _, _, _, _, _ := popoutIdent("map", "")
	popoutsMu.Lock()
	popoutsLocked = locked
	savePopoutStoreLocked() // persist so the lock survives an app restart
	var wins []*application.WebviewWindow
	for name, w := range popouts {
		// The map stays interactive (pan/zoom/buttons); lock only timer overlays.
		if name != mapName {
			wins = append(wins, w)
		}
	}
	popoutsMu.Unlock()
	for _, w := range wins {
		w.SetIgnoreMouseEvents(locked)
	}
	if v3App != nil {
		if locked {
			v3App.Event.Emit("popouts-locked")
		} else {
			v3App.Event.Emit("popouts-unlocked")
		}
	}
}

// popoutState is the persisted record for one overlay. Open marks whether it
// should be reopened on the next launch; geometry is remembered even after a
// close so a later manual reopen lands where the user last left it.
type popoutState struct {
	Kind     string `json:"kind"`     // "map" | "timers"
	Category string `json:"category"` // timer category name (empty for the map)
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Open     bool   `json:"open"`
}

func popoutStorePath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "popouts.json")
}

// popoutStoreFile is the on-disk form: the per-overlay set plus the global Lock
// toggle (so it survives a restart).
type popoutStoreFile struct {
	Locked  bool                    `json:"locked"`
	Popouts map[string]*popoutState `json:"popouts"`
}

// LoadPopoutStore reads the persisted overlay set + lock state. Called once at
// startup before ReopenSavedPopouts.
func LoadPopoutStore() {
	data, err := os.ReadFile(popoutStorePath())
	if err != nil {
		return
	}
	var f popoutStoreFile
	if json.Unmarshal(data, &f) == nil && f.Popouts != nil {
		popoutsMu.Lock()
		popoutSt = f.Popouts
		popoutsLocked = f.Locked
		popoutsMu.Unlock()
		return
	}
	// Backward-compat: older files were a bare map[string]*popoutState.
	m := map[string]*popoutState{}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	popoutsMu.Lock()
	popoutSt = m
	popoutsMu.Unlock()
}

// savePopoutStoreLocked writes the overlay set + lock state. Caller holds popoutsMu.
func savePopoutStoreLocked() {
	path := popoutStorePath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(popoutStoreFile{Locked: popoutsLocked, Popouts: popoutSt}, "", "  ")
	_ = os.WriteFile(path, data, 0600)
}

// popoutIdent resolves a (kind, category) pair to a stable window name plus the
// window's title, URL hash, and default/min geometry.
func popoutIdent(kind, category string) (name, title, hash string, defW, defH, minW, minH int) {
	if kind == "timers" {
		return "popout-timers-" + category,
			category,
			"#popout=timers&category=" + url.QueryEscape(category),
			340, 240, 200, 120
	}
	return "popout-map", "Map", "#popout=map", 460, 420, 260, 220
}

// OpenPopout opens (or focuses) an overlay window. kind is "map" or "timers";
// category is the timer category name for kind "timers" (ignored for the map).
// Bound to the frontend so the pop-out buttons can call it.
func (a *App) OpenPopout(kind, category string) {
	openPopoutWindow(kind, category, true)
}

// SavePopoutState records an overlay's current geometry (reported by the
// frontend as it's dragged/resized). Bound to the frontend.
func (a *App) SavePopoutState(kind, category string, x, y, w, h int) {
	name, _, _, _, _, _, _ := popoutIdent(kind, category)
	popoutsMu.Lock()
	st := popoutSt[name]
	if st == nil {
		st = &popoutState{Kind: kind, Category: category}
		popoutSt[name] = st
	}
	st.X, st.Y, st.W, st.H = x, y, w, h
	st.Open = true
	savePopoutStoreLocked()
	popoutsMu.Unlock()
}

// ReopenSavedPopouts recreates every overlay that was open when the app last
// ran, at its saved geometry. Called once the application is ready.
func ReopenSavedPopouts() {
	popoutsMu.Lock()
	var toOpen []*popoutState
	for _, st := range popoutSt {
		if st.Open {
			toOpen = append(toOpen, st)
		}
	}
	popoutsMu.Unlock()
	for _, st := range toOpen {
		// Don't steal focus from the game on launch.
		openPopoutWindow(st.Kind, st.Category, false)
	}
}

// openPopoutWindow does the actual create-or-focus. focus controls whether a
// freshly created window grabs focus (true for a user click, false on startup).
func openPopoutWindow(kind, category string, focus bool) {
	if v3App == nil {
		return
	}
	name, title, hash, defW, defH, minW, minH := popoutIdent(kind, category)

	popoutsMu.Lock()
	if w, ok := popouts[name]; ok {
		popoutsMu.Unlock()
		w.Show()
		if focus {
			w.Focus()
		}
		return
	}

	// Start from the saved geometry if we have a usable one; otherwise defaults.
	st := popoutSt[name]
	width, height := defW, defH
	initPos := application.WindowCentered
	var x, y int
	if st != nil && st.W >= minW && st.H >= minH {
		width, height = st.W, st.H
		x, y = st.X, st.Y
		initPos = application.WindowXY
	}
	// Mark open and persist before creating so a crash still remembers it.
	if st == nil {
		st = &popoutState{Kind: kind, Category: category}
		popoutSt[name] = st
	}
	st.Kind, st.Category, st.Open = kind, category, true
	savePopoutStoreLocked()
	popoutsMu.Unlock()

	w := v3App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            name,
		Title:           title,
		Width:           width,
		Height:          height,
		MinWidth:        minW,
		MinHeight:       minH,
		InitialPosition: initPos,
		X:               x,
		Y:               y,
		Frameless:       true,
		// Default on; the overlay's settings panel can turn it off at runtime.
		AlwaysOnTop: true,
		// Transparent composition so the game shows through at whatever
		// background opacity the user picks in the overlay's settings.
		BackgroundType: application.BackgroundTypeTransparent,
		URL:            "/" + hash,
		Windows: application.WindowsWindow{
			Theme: application.Dark,
			// Overlays are managed from the main window/tray, not the taskbar.
			HiddenOnTaskbar: true,
			// The overlay draws its own border/rounded corners; DWM
			// decorations leave shadow artifacts on transparent windows.
			DisableFramelessWindowDecorations: true,
		},
	})

	popoutsMu.Lock()
	popouts[name] = w
	popoutsMu.Unlock()

	// Closing an overlay really closes it (unlike the main window, which hides):
	// drop it from the live map and mark it closed so it won't reopen next launch.
	w.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		popoutsMu.Lock()
		delete(popouts, name)
		// A user closing the overlay marks it closed; an app quit leaves the
		// open set intact so it reopens next launch.
		if !appQuitting {
			if s := popoutSt[name]; s != nil {
				s.Open = false
				savePopoutStoreLocked()
			}
		}
		popoutsMu.Unlock()
	})

	w.Show()
	if focus {
		w.Focus()
	}
	// If timer overlays are currently locked, lock this newly-opened one too
	// (the map is never locked).
	popoutsMu.Lock()
	lk := popoutsLocked
	popoutsMu.Unlock()
	if lk && kind != "map" {
		w.SetIgnoreMouseEvents(true)
	}
}

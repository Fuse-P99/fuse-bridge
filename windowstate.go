package main

// The main window remembers where the user left it.
//
// Geometry is captured from the window's own move/resize events (debounced —
// a drag fires them continuously), written to window.json, and fed back as the
// creation options on the next launch. Coordinates are stored ABSOLUTE rather
// than relative to a screen's work area: absolute coordinates name the monitor,
// which is the part a multi-head user notices when it's lost.
//
// An in-place update is just a relaunch of the same binary, so it inherits all
// of this — provided the pending write actually reaches disk first. applyUpdate
// exits via os.Exit and so flushes explicitly, the same way it already does for
// trigger timers.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	mainWinDefaultW = 900
	mainWinDefaultH = 650
	// The window's own minimums; a restored size is clamped to them so a
	// corrupt or hand-edited file can't produce an unusable sliver.
	mainWinMinW = 700
	mainWinMinH = 500
	// Long enough that a drag writes once when it settles, short enough that
	// losing the window to a hard kill a moment later costs nothing visible.
	mainWinFlushDelay = 700 * time.Millisecond
	// Minimum overlap with a monitor's work area for a window to count as
	// reachable — enough title bar to grab. Shared with the overlays.
	winVisibleMinX = 60
	winVisibleMinY = 40
)

// windowGeom is the persisted form. Maximised is tracked apart from the
// rectangle: while maximised the OS reports the maximised bounds, so the stored
// rectangle stays the last NORMAL one — the size to restore down to.
type windowGeom struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	W         int  `json:"w"`
	H         int  `json:"h"`
	Maximised bool `json:"maximised"`
	HasPos    bool `json:"has_pos"` // false until the window has been placed once
}

var (
	winGeomMu     sync.Mutex
	winGeom       = windowGeom{W: mainWinDefaultW, H: mainWinDefaultH}
	winGeomDirty  bool
	winFlushTimer *time.Timer
)

func windowGeomPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "window.json")
}

// LoadMainWindowGeom restores the saved geometry. Call before the window is
// created; a missing or unusable file leaves the defaults in place.
func LoadMainWindowGeom() {
	data, err := os.ReadFile(windowGeomPath())
	if err != nil {
		return
	}
	var g windowGeom
	if json.Unmarshal(data, &g) != nil {
		return
	}
	if g.W < mainWinMinW {
		g.W = mainWinDefaultW
	}
	if g.H < mainWinMinH {
		g.H = mainWinDefaultH
	}
	winGeomMu.Lock()
	winGeom = g
	winGeomMu.Unlock()
}

// MainWindowGeom returns the geometry the window should open with.
func MainWindowGeom() windowGeom {
	winGeomMu.Lock()
	defer winGeomMu.Unlock()
	return winGeom
}

// FlushMainWindowGeom writes pending geometry now. A no-op when nothing changed,
// so the quit and update paths can both call it unconditionally.
func FlushMainWindowGeom() {
	winGeomMu.Lock()
	if !winGeomDirty {
		winGeomMu.Unlock()
		return
	}
	winGeomDirty = false
	g := winGeom
	winGeomMu.Unlock()

	path := windowGeomPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(g, "", "  ")
	_ = os.WriteFile(path, data, 0600)
}

// scheduleWinGeomFlushLocked debounces the write so a drag costs one file
// update instead of hundreds. Caller holds winGeomMu.
func scheduleWinGeomFlushLocked() {
	if winFlushTimer == nil {
		winFlushTimer = time.AfterFunc(mainWinFlushDelay, FlushMainWindowGeom)
		return
	}
	winFlushTimer.Reset(mainWinFlushDelay)
}

// captureMainWindowGeom records the window's current rectangle. Every window
// accessor is read BEFORE the mutex is taken: they hop to the main thread, and
// holding the mutex across that hop would deadlock against a hook already
// running there.
func captureMainWindowGeom() {
	if mainWindow == nil || mainWindow.IsMinimised() {
		return // a minimised window reports garbage (-32000 on Windows)
	}
	maximised := mainWindow.IsMaximised()
	var x, y, w, h int
	if !maximised {
		x, y = mainWindow.Position()
		w, h = mainWindow.Size()
	}

	winGeomMu.Lock()
	defer winGeomMu.Unlock()
	changed := winGeom.Maximised != maximised
	winGeom.Maximised = maximised
	if !maximised && w >= mainWinMinW && h >= mainWinMinH && x > -30000 && y > -30000 {
		if !winGeom.HasPos || winGeom.X != x || winGeom.Y != y ||
			winGeom.W != w || winGeom.H != h {
			winGeom.X, winGeom.Y, winGeom.W, winGeom.H = x, y, w, h
			winGeom.HasPos = true
			changed = true
		}
	}
	if changed {
		winGeomDirty = true
		scheduleWinGeomFlushLocked()
	}
}

// trackMainWindow wires the window's move/resize events to the store.
func trackMainWindow(w *application.WebviewWindow) {
	w.RegisterHook(events.Common.WindowDidMove, func(*application.WindowEvent) {
		captureMainWindowGeom()
	})
	w.RegisterHook(events.Common.WindowDidResize, func(*application.WindowEvent) {
		captureMainWindowGeom()
	})
}

// rectVisibleOnScreen reports whether a window rectangle overlaps some
// monitor's work area by enough to be seen and grabbed. Screens can only be
// enumerated once the app is running; when they can't be, this answers true —
// leaving a window where it is beats moving it on a guess.
func rectVisibleOnScreen(x, y, w, h int) bool {
	if v3App == nil {
		return true
	}
	screens := v3App.Screen.GetAll()
	if len(screens) == 0 {
		return true
	}
	for _, s := range screens {
		if s == nil {
			continue
		}
		a := s.WorkArea
		overX := min(x+w, a.X+a.Width) - max(x, a.X)
		overY := min(y+h, a.Y+a.Height) - max(y, a.Y)
		if overX >= winVisibleMinX && overY >= winVisibleMinY {
			return true
		}
	}
	return false
}

// ensureMainWindowOnScreen rescues a window restored onto a monitor that no
// longer exists — an undocked laptop, a resolution change. Without it the app
// would come back invisible with no way to reach it short of deleting
// window.json, which is exactly the failure that makes people distrust
// remembered positions.
func ensureMainWindowOnScreen() {
	if mainWindow == nil || v3App == nil {
		return
	}
	x, y := mainWindow.Position()
	w, h := mainWindow.Size()
	if rectVisibleOnScreen(x, y, w, h) {
		return
	}
	mainWindow.Center()
	captureMainWindowGeom()
}

// applyMainWindowState finishes the restore once the app is running: screens
// can only be enumerated now, and maximising here avoids racing the initial
// placement pass, which would drag a maximised window back down.
func applyMainWindowState() {
	if mainWindow == nil {
		return
	}
	ensureMainWindowOnScreen()
	if MainWindowGeom().Maximised {
		mainWindow.Maximise()
	}
}

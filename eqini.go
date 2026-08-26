package main

// Reusable access to the INI files EQ itself writes: eqclient.ini (window
// mode, resolution, window placement) and the per-character
// UI_<Char>_project1999.ini (viewport, UI layout). Parsed generically and
// cached by ModTime so any part of the app can consult them cheaply —
// Racing Mode reads the game-window and viewport rects from here, and future
// features are expected to pull other keys through the same helpers.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// iniFile is a parsed INI: lower(section) → lower(key) → raw value. Keys that
// appear before any [Section] header land under the "" section.
type iniFile map[string]map[string]string

type iniCacheEntry struct {
	mod  time.Time
	data iniFile
}

var (
	iniMu    sync.Mutex
	iniCache = map[string]iniCacheEntry{}
)

// ReadINIFile parses an INI file, cached against its ModTime. Returns nil when
// the file is missing or unreadable.
func ReadINIFile(path string) iniFile {
	if path == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	iniMu.Lock()
	if e, ok := iniCache[path]; ok && e.mod.Equal(fi.ModTime()) {
		iniMu.Unlock()
		return e.data
	}
	iniMu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	f := iniFile{}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		sec := f[section]
		if sec == nil {
			sec = map[string]string{}
			f[section] = sec
		}
		sec[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}

	iniMu.Lock()
	iniCache[path] = iniCacheEntry{mod: fi.ModTime(), data: f}
	iniMu.Unlock()
	return f
}

// IniGet reads a key. section "" searches every section — EQ's key names are
// distinctive enough that callers rarely need to know which header a setting
// sits under (and eqclient.ini has moved keys between sections over the years).
func IniGet(f iniFile, section, key string) (string, bool) {
	if f == nil {
		return "", false
	}
	key = strings.ToLower(key)
	if section != "" {
		v, ok := f[strings.ToLower(section)][key]
		return v, ok
	}
	for _, sec := range f {
		if v, ok := sec[key]; ok {
			return v, true
		}
	}
	return "", false
}

// IniInt reads an integer key, def when missing or malformed.
func IniInt(f iniFile, section, key string, def int) int {
	s, ok := IniGet(f, section, key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// IniBool reads a TRUE/FALSE key (EQ's convention), def when missing.
func IniBool(f iniFile, section, key string, def bool) bool {
	s, ok := IniGet(f, section, key)
	if !ok {
		return def
	}
	return strings.EqualFold(strings.TrimSpace(s), "true")
}

// EQClientINI returns the parsed eqclient.ini (nil when unavailable).
func EQClientINI() iniFile {
	return ReadINIFile(eqRootFilePath(GetSettings().EQDirectory, "eqclient.ini"))
}

// CharUIINI returns the parsed UI_<Char>_project1999.ini for a character.
func CharUIINI(char string) iniFile {
	char = strings.TrimSpace(char)
	if char == "" {
		return nil
	}
	return ReadINIFile(eqRootFilePath(GetSettings().EQDirectory, "UI_"+char+"_project1999.ini"))
}

// EQGameWindow reports the game's client-area rect in screen coordinates and
// its render resolution, from eqclient.ini. Fullscreen mode fills the primary
// monitor from (0,0) at [VideoMode] Width/Height; windowed mode sits at the
// WindowedModeX/YOffset with the Windowed dimensions.
func EQGameWindow() (x, y, w, h int, windowed, ok bool) {
	f := EQClientINI()
	if f == nil {
		return 0, 0, 0, 0, false, false
	}
	windowed = IniBool(f, "", "windowedmode", false)
	if windowed {
		w = IniInt(f, "videomode", "windowedwidth", 0)
		h = IniInt(f, "videomode", "windowedheight", 0)
		x = IniInt(f, "", "windowedmodexoffset", 0)
		y = IniInt(f, "", "windowedmodeyoffset", 0)
	} else {
		w = IniInt(f, "videomode", "width", 0)
		h = IniInt(f, "videomode", "height", 0)
	}
	return x, y, w, h, windowed, w > 0 && h > 0
}

// CharViewport reports a character's in-game 3D viewport rect (relative to the
// game's client area) for a given render resolution, from the character's UI
// ini ([ViewPort<W>x<H>] X/Y/W/H). ok is false when the file or section is
// missing — callers fall back to the full resolution.
func CharViewport(char string, resW, resH int) (vx, vy, vw, vh int, ok bool) {
	f := CharUIINI(char)
	if f == nil {
		return 0, 0, 0, 0, false
	}
	section := fmt.Sprintf("viewport%dx%d", resW, resH)
	vx = IniInt(f, section, "x", 0)
	vy = IniInt(f, section, "y", 0)
	vw = IniInt(f, section, "w", 0)
	vh = IniInt(f, section, "h", 0)
	return vx, vy, vw, vh, vw > 0 && vh > 0
}

// RacingOverlayRect composes the two: the screen rect of the logged-in
// character's viewport, which is exactly where the Racing Mode overlay window
// belongs. Falls back to the full client area when the character's UI ini has
// no matching viewport section.
func RacingOverlayRect() (x, y, w, h int, ok bool) {
	wx, wy, rw, rh, _, wok := EQGameWindow()
	if !wok {
		return 0, 0, 0, 0, false
	}
	vx, vy, vw, vh, vok := CharViewport(currentCharName, rw, rh)
	if !vok {
		vx, vy, vw, vh = 0, 0, rw, rh
	}
	return wx + vx, wy + vy, vw, vh, true
}

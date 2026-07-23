package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Overlay popout windows: the map and one window per timer category, popped out
// of the main UI as frameless, transparent, always-on-top game overlays.
//
// Persistence (popouts.json) splits two ways on purpose:
//   - The MAP overlay is app-wide. Its open state, geometry and look settings are
//     shared by every character — you want the map in the same place regardless
//     of who you're playing.
//   - TIMER overlays are per character, so each toon keeps its own set, positions
//     and sizes. A character logging in for the first time inherits the layout of
//     a configured same-class character, else starts with nothing.
//
// Per-overlay look settings (background colour/opacity, always-on-top) live in
// the frontend's localStorage, keyed by character for timer overlays and shared
// for the map. Timer overlays are closed and reopened on a character swap, so
// each one re-reads the incoming character's settings when it mounts.
var (
	popoutsMu sync.Mutex
	popouts   = map[string]*application.WebviewWindow{} // live windows by name

	popoutMapSt         *popoutState                           // app-wide map overlay
	popoutSpecials      = map[string]*popoutState{}            // app-wide raid-section overlays, by window name
	popoutChars         = map[string]map[string]*popoutState{} // lower(char) → window name → state
	popoutCharClass     = map[string]string{}                  // lower(char) → class, for same-class inheritance
	popoutActiveChar    string                                 // lower(char) whose timer overlays are showing
	popoutSwitchClosing = map[string]bool{}                    // windows being closed by a character switch
	popoutsLocked       bool                                   // Lock toggle, persisted; guarded by popoutsMu
	appQuitting         bool                                   // guarded by popoutsMu
)

// popoutSeedKey parks a pre-per-character layout until the first character logs
// in and adopts it, so upgrading doesn't discard an existing overlay setup.
const popoutSeedKey = "*"

// isSpecialPopoutKind reports whether kind is one of the "Special Overlays" —
// live raid-card sections popped out as overlays. Like the map they are
// app-wide (raid state isn't per character): one instance, shared geometry and
// look settings, untouched by character swaps.
func isSpecialPopoutKind(kind string) bool {
	switch kind {
	case "raidassign", "raiddebuffs", "raidclerics", "voicespeakers":
		return true
	}
	return false
}

// specialPopoutNames returns the window names of every special overlay kind,
// for skipping them in per-character open/close sweeps.
func specialPopoutNames() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"raidassign", "raiddebuffs", "raidclerics", "voicespeakers"} {
		n, _, _, _, _, _, _ := popoutIdent(k, "")
		out[n] = true
	}
	return out
}

// popoutsCanOpen reports whether it's safe to create an overlay window.
//
// v3App is non-nil from application.New() onward — well before Run() starts the
// main loop — so a nil check alone is not enough. A window created in that gap
// comes up without transparent composition and renders as an opaque white panel.
// The tailer can reach ApplyPopoutsForCharacter during that gap (it attaches to
// the log independently of UI startup), so window creation waits for
// ApplicationStarted; the handler there re-applies the current character's
// layout, so nothing is lost by skipping early.
func popoutsCanOpen() bool {
	if v3App == nil {
		return false
	}
	select {
	case <-wailsReady:
		return true
	default:
		return false
	}
}

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

// GetOverlayTitleMode returns when overlay title bars are shown: "always"
// (default), "locked" (hidden while overlays are locked), or "zero" (shown only
// while a timer/alert is active).
func (a *App) GetOverlayTitleMode() string {
	switch GetSettings().OverlayTitles {
	case "locked":
		return "locked"
	case "zero":
		return "zero"
	default:
		return "always"
	}
}

// SetOverlayTitleMode persists the title-bar mode and notifies open overlays so
// they update live.
func (a *App) SetOverlayTitleMode(mode string) {
	switch mode {
	case "always", "locked", "zero":
	default:
		mode = "always"
	}
	s := GetSettings()
	s.OverlayTitles = mode
	UpdateSettings(s)
	if v3App != nil {
		v3App.Event.Emit("overlay-titles")
	}
}

// GetSnapToGrid reports whether overlays snap to the 10px grid on move/resize.
func (a *App) GetSnapToGrid() bool { return GetSettings().SnapToGrid }

// SetSnapToGrid persists the snap-to-grid toggle and notifies open overlays.
func (a *App) SetSnapToGrid(on bool) {
	s := GetSettings()
	s.SnapToGrid = on
	UpdateSettings(s)
	if v3App != nil {
		v3App.Event.Emit("snap-grid")
	}
}

// popoutState is the persisted record for one overlay. Open marks whether it
// should be reopened on the next launch; geometry is remembered even after a
// close so a later manual reopen lands where the user last left it.
type popoutState struct {
	Kind     string `json:"kind"`     // "map" | "timers" | "alerts"
	Category string `json:"category"` // trigger category name (empty for the map)
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

// popoutStoreFile is the on-disk form: the app-wide map overlay, the per-character
// timer overlays, each configured character's class (for same-class inheritance),
// and the global Lock toggle.
type popoutStoreFile struct {
	Locked    bool                               `json:"locked"`
	Map       *popoutState                       `json:"map,omitempty"`
	Specials  map[string]*popoutState            `json:"specials,omitempty"` // app-wide raid-section overlays
	Chars     map[string]map[string]*popoutState `json:"chars,omitempty"`
	CharClass map[string]string                  `json:"char_class,omitempty"`
	// Popouts is the pre-per-character app-wide layout, migrated on load.
	Popouts map[string]*popoutState `json:"popouts,omitempty"`
}

// LoadPopoutStore reads the persisted overlay layout + lock state. Called once at
// startup before ReopenSavedPopouts.
func LoadPopoutStore() {
	data, err := os.ReadFile(popoutStorePath())
	if err != nil {
		return
	}
	var f popoutStoreFile
	if json.Unmarshal(data, &f) != nil {
		// Oldest format: a bare map[string]*popoutState.
		m := map[string]*popoutState{}
		if json.Unmarshal(data, &m) != nil {
			return
		}
		f.Popouts = m
	}

	popoutsMu.Lock()
	defer popoutsMu.Unlock()
	popoutsLocked = f.Locked
	popoutMapSt = f.Map
	if popoutSpecials = f.Specials; popoutSpecials == nil {
		popoutSpecials = map[string]*popoutState{}
	}
	if popoutChars = f.Chars; popoutChars == nil {
		popoutChars = map[string]map[string]*popoutState{}
	}
	if popoutCharClass = f.CharClass; popoutCharClass == nil {
		popoutCharClass = map[string]string{}
	}
	// Migrate a pre-per-character layout: its map entry becomes the app-wide map,
	// and its timer overlays are parked as the seed layout that the first
	// character to log in adopts — upgrading shouldn't wipe an existing setup.
	for name, st := range f.Popouts {
		if st == nil {
			continue
		}
		if st.Kind == "map" || name == "popout-map" {
			if popoutMapSt == nil {
				popoutMapSt = st
			}
			continue
		}
		if popoutChars[popoutSeedKey] == nil {
			popoutChars[popoutSeedKey] = map[string]*popoutState{}
		}
		popoutChars[popoutSeedKey][name] = st
	}
}

// savePopoutStoreLocked writes the overlay layout + lock state. Caller holds popoutsMu.
func savePopoutStoreLocked() {
	path := popoutStorePath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(popoutStoreFile{
		Locked:    popoutsLocked,
		Map:       popoutMapSt,
		Specials:  popoutSpecials,
		Chars:     popoutChars,
		CharClass: popoutCharClass,
	}, "", "  ")
	_ = os.WriteFile(path, data, 0600)
}

// popoutStateForLocked returns the stored record for one overlay, creating it
// when create is true. The map is app-wide; timer overlays belong to the active
// character (and are not stored at all until a character is known).
// Caller holds popoutsMu.
func popoutStateForLocked(kind, category, name string, create bool) *popoutState {
	if kind == "map" {
		if popoutMapSt == nil && create {
			popoutMapSt = &popoutState{Kind: "map"}
		}
		return popoutMapSt
	}
	// Special overlays are app-wide like the map, keyed by window name.
	if isSpecialPopoutKind(kind) {
		st := popoutSpecials[name]
		if st == nil && create {
			st = &popoutState{Kind: kind}
			popoutSpecials[name] = st
		}
		return st
	}
	return popoutCharStateLocked(popoutActiveChar, kind, category, name, create)
}

// popoutCharStateLocked returns one character's record for a timer overlay.
// Caller holds popoutsMu.
func popoutCharStateLocked(charKey, kind, category, name string, create bool) *popoutState {
	if charKey == "" {
		return nil
	}
	set := popoutChars[charKey]
	if set == nil {
		if !create {
			return nil
		}
		set = map[string]*popoutState{}
		popoutChars[charKey] = set
	}
	st := set[name]
	if st == nil && create {
		st = &popoutState{Kind: kind, Category: category}
		set[name] = st
	}
	return st
}

func clonePopoutSet(in map[string]*popoutState) map[string]*popoutState {
	out := make(map[string]*popoutState, len(in))
	for k, v := range in {
		if v == nil {
			continue
		}
		c := *v
		out[k] = &c
	}
	return out
}

// classForCharacter returns a character's class from the local cache, or "" when
// it hasn't been resolved yet (common on a brand-new toon's first login — see
// RetryPopoutClassInheritance).
func classForCharacter(name string) string {
	if name == "" {
		return ""
	}
	if ci, ok := cachedCharInfos([]string{name})[strings.ToLower(name)]; ok {
		return ci.Class
	}
	return ""
}

// sameClassLayoutLocked finds a configured character of the same class whose
// timer layout a new character can inherit, or nil. Caller holds popoutsMu.
func sameClassLayoutLocked(key, charName string) map[string]*popoutState {
	cls := classForCharacter(charName)
	if cls == "" {
		return nil
	}
	for k, set := range popoutChars {
		if k == key || k == popoutSeedKey || len(set) == 0 {
			continue
		}
		if strings.EqualFold(popoutCharClass[k], cls) {
			return set
		}
	}
	return nil
}

// seedLayoutForLocked gives a first-time character a starting layout: a migrated
// pre-per-character setup if one is parked, else a same-class character's layout.
// Returns true when one was adopted. Caller holds popoutsMu.
func seedLayoutForLocked(key, charName string) bool {
	if popoutChars[key] != nil {
		return false // already configured
	}
	if seed := popoutChars[popoutSeedKey]; len(seed) > 0 {
		popoutChars[key] = clonePopoutSet(seed)
		delete(popoutChars, popoutSeedKey)
		return true
	}
	if donor := sameClassLayoutLocked(key, charName); donor != nil {
		popoutChars[key] = clonePopoutSet(donor)
		return true
	}
	return false
}

// popoutIdent resolves a (kind, category) pair to a stable window name plus the
// window's title, URL hash, and default/min geometry.
func popoutIdent(kind, category string) (name, title, hash string, defW, defH, minW, minH int) {
	switch kind {
	// Special Overlays: live raid-card sections. App-wide, one instance each.
	case "raidassign":
		return "popout-raidassign", "Raid Assignments", "#popout=raidassign", 320, 280, 220, 140
	case "raiddebuffs":
		return "popout-raiddebuffs", "Raid Debuffs", "#popout=raiddebuffs", 300, 240, 200, 120
	case "raidclerics":
		return "popout-raidclerics", "Raid Clerics", "#popout=raidclerics", 300, 280, 200, 140
	case "voicespeakers":
		return "popout-voicespeakers", "Voice Speakers", "#popout=voicespeakers", 260, 160, 180, 80
	case "timers":
		return "popout-timers-" + category,
			category,
			"#popout=timers&category=" + url.QueryEscape(category),
			340, 240, 200, 120
	case "alerts":
		// Alert overlays are short and wide: a stack of one-line messages.
		return "popout-alerts-" + category,
			category,
			"#popout=alerts&category=" + url.QueryEscape(category),
			420, 150, 220, 70
	}
	return "popout-map", "Map", "#popout=map", 460, 420, 260, 220
}

// GetPopoutProfile tells a timer overlay whose look settings to load: "char" is
// the active character, and "donor" is a configured same-class character to seed
// from the first time this one is used (so an alt doesn't start from raw
// defaults). Both are lowercased keys. The map overlay ignores this — its look
// settings are app-wide. Bound to the frontend.
func (a *App) GetPopoutProfile() map[string]string {
	popoutsMu.Lock()
	defer popoutsMu.Unlock()
	char := popoutActiveChar
	donor := ""
	if cls := popoutCharClass[char]; cls != "" {
		for k, set := range popoutChars {
			if k == char || k == popoutSeedKey || len(set) == 0 {
				continue
			}
			if strings.EqualFold(popoutCharClass[k], cls) {
				donor = k
				break
			}
		}
	}
	return map[string]string{"char": char, "donor": donor}
}

// OpenPopout opens (or focuses) an overlay window. kind is "map", "timers",
// "alerts", or a special raid kind; category is the trigger category name
// (ignored otherwise). Bound to the frontend so the pop-out buttons can call it.
func (a *App) OpenPopout(kind, category string) {
	// Popping something out means the user wants to SEE it: clear any global
	// hide (manual Hide Windows or camp-out auto-hide) first, so the new
	// overlay can't be born into a hidden set and confusingly never appear.
	popoutVisMu.Lock()
	unhide := popoutsManualHidden || popoutsAutoHidden
	popoutsManualHidden = false
	popoutsAutoHidden = false
	popoutVisMu.Unlock()
	if unhide {
		applyPopoutVisibility()
		if v3App != nil {
			// The Timers tab syncs its Hide/Show button label off this.
			v3App.Event.Emit("popouts-unhidden")
		}
	}
	openPopoutWindow(kind, category, true)
}

// SavePopoutState records an overlay's current geometry (reported by the
// frontend as it's dragged/resized). Bound to the frontend.
func (a *App) SavePopoutState(kind, category string, x, y, w, h int) {
	name, _, _, _, _, _, _ := popoutIdent(kind, category)
	popoutsMu.Lock()
	if st := popoutStateForLocked(kind, category, name, true); st != nil {
		st.Kind, st.Category = kind, category
		st.X, st.Y, st.W, st.H = x, y, w, h
		st.Open = true
		savePopoutStoreLocked()
	}
	popoutsMu.Unlock()
}

// ReopenSavedPopouts reopens the app-wide overlays that were open — the map and
// any special raid-section overlays. Timer overlays are per character, so
// they're opened by ApplyPopoutsForCharacter once the tailed character is
// known. Called once the application is ready.
func ReopenSavedPopouts() {
	popoutsMu.Lock()
	open := popoutMapSt != nil && popoutMapSt.Open
	var specials []string
	for _, st := range popoutSpecials {
		if st != nil && st.Open && isSpecialPopoutKind(st.Kind) {
			specials = append(specials, st.Kind)
		}
	}
	popoutsMu.Unlock()
	if open {
		openPopoutWindow("map", "", false) // don't steal focus from the game
	}
	for _, kind := range specials {
		openPopoutWindow(kind, "", false)
	}
}

// startupPopoutsPending is set at app start to defer opening every overlay —
// the app-wide map and the tailed character's timer overlays. The tailer names
// the most recently played toon even on a restart while EQ is closed, so
// opening anything immediately would splash overlays over the desktop.
// maybeApplyDeferredPopouts opens them only once the log produces a fresh line
// after launch (i.e. the toon is being played).
var startupPopoutsPending atomic.Bool

// maybeApplyDeferredPopouts opens the overlays that app start deferred — the
// app-wide map and the tailed character's timers — once the log is actively
// written. Cheap to call per log line: it's a single atomic load until a
// startup defer is actually pending, and fires the apply exactly once.
func maybeApplyDeferredPopouts() {
	if !startupPopoutsPending.Load() || !popoutsCanOpen() {
		return
	}
	if !startupPopoutsPending.CompareAndSwap(true, false) {
		return
	}
	ReopenSavedPopouts() // app-wide map overlay
	if currentCharName != "" {
		ApplyPopoutsForCharacter(currentCharName)
	}
}

// ApplyPopoutsForCharacter switches the timer overlays to charName's saved
// layout: every timer overlay is closed and the incoming character's set is
// reopened at its own geometry (reopening also makes each overlay re-read that
// character's look settings when it mounts). The map overlay is app-wide and is
// left untouched. Called on login and on every character swap.
func ApplyPopoutsForCharacter(charName string) {
	// Deliberately checked before popoutActiveChar is touched: skipping early must
	// leave the state untouched so the deferred open still applies this character's
	// layout later. While startupPopoutsPending is set (app just launched), the
	// tailer attaches to the most-recent eqlog and names the last-played toon even
	// with EQ closed / at char-select — opening overlays then would splash them
	// over the desktop. maybeApplyDeferredPopouts clears the flag and re-invokes us
	// once a fresh log line proves the toon is actually being played.
	if !popoutsCanOpen() || startupPopoutsPending.Load() || charName == "" {
		return
	}
	key := strings.ToLower(charName)
	mapName, _, _, _, _, _, _ := popoutIdent("map", "")

	popoutsMu.Lock()
	if popoutActiveChar == key {
		popoutsMu.Unlock()
		return
	}
	popoutActiveChar = key
	seedLayoutForLocked(key, charName)
	if cls := classForCharacter(charName); cls != "" {
		popoutCharClass[key] = cls
	}

	// What this character wants open.
	var toOpen []*popoutState
	for _, st := range popoutChars[key] {
		if st != nil && st.Open {
			toOpen = append(toOpen, st)
		}
	}
	// Close every live timer overlay (the outgoing character's). Flagged as a
	// switch-close so the close hook leaves that character's saved layout intact —
	// their overlays must come back when they log in again. The map and the
	// special raid overlays are app-wide and stay open across swaps.
	specialNames := specialPopoutNames()
	var toClose []*application.WebviewWindow
	for n, w := range popouts {
		if n == mapName || specialNames[n] {
			continue
		}
		popoutSwitchClosing[n] = true
		delete(popouts, n)
		toClose = append(toClose, w)
	}
	savePopoutStoreLocked()
	popoutsMu.Unlock()

	for _, w := range toClose {
		w.Close()
	}
	for _, st := range toOpen {
		openPopoutWindow(st.Kind, st.Category, false)
	}
}

// RetryPopoutClassInheritance re-runs same-class inheritance once a character's
// class resolves. The class cache is usually empty on a brand-new toon's first
// login, so ApplyPopoutsForCharacter can't inherit at that moment; this gives the
// alt its same-class layout a beat later instead of not at all. No-op if the
// character already has a layout or isn't the active one.
func RetryPopoutClassInheritance(charName string) {
	// Same startup guard as ApplyPopoutsForCharacter: don't open overlays while the
	// first fresh-line deferral is still pending (an async class fetch during
	// startup can otherwise reach here with EQ closed / at char-select).
	if !popoutsCanOpen() || startupPopoutsPending.Load() || charName == "" {
		return
	}
	key := strings.ToLower(charName)

	popoutsMu.Lock()
	if key != popoutActiveChar || popoutChars[key] != nil {
		if cls := classForCharacter(charName); cls != "" && popoutCharClass[key] != cls {
			popoutCharClass[key] = cls
			savePopoutStoreLocked()
		}
		popoutsMu.Unlock()
		return
	}
	adopted := seedLayoutForLocked(key, charName)
	if cls := classForCharacter(charName); cls != "" {
		popoutCharClass[key] = cls
	}
	var toOpen []*popoutState
	if adopted {
		for _, st := range popoutChars[key] {
			if st != nil && st.Open {
				toOpen = append(toOpen, st)
			}
		}
	}
	savePopoutStoreLocked()
	popoutsMu.Unlock()

	for _, st := range toOpen {
		openPopoutWindow(st.Kind, st.Category, false)
	}
}

// openPopoutWindow does the actual create-or-focus. focus controls whether a
// freshly created window grabs focus (true for a user click, false on startup).
func openPopoutWindow(kind, category string, focus bool) {
	// Not merely a nil check — see popoutsCanOpen: creating a window before the
	// main loop is running produces an opaque (white) overlay.
	if !popoutsCanOpen() {
		return
	}
	name, title, hash, defW, defH, minW, minH := popoutIdent(kind, category)

	popoutsMu.Lock()
	if w, ok := popouts[name]; ok {
		popoutsMu.Unlock()
		w.Show()
		if focus {
			w.Focus()
			// Already popped out — never create a duplicate. Flash the existing
			// overlay's title bar (red → normal) so the user can spot it.
			if v3App != nil {
				v3App.Event.Emit("popout-flash", name)
			}
		}
		return
	}

	// Start from the saved geometry if we have a usable one; otherwise defaults.
	// Mark open and persist before creating so a crash still remembers it. The
	// owning character is captured for the close hook: a timer overlay belongs to
	// whoever was active when it opened, not to whoever is active when it closes.
	owner := popoutActiveChar
	st := popoutStateForLocked(kind, category, name, true)
	width, height := defW, defH
	initPos := application.WindowCentered
	var x, y int
	if st != nil {
		if st.W >= minW && st.H >= minH {
			width, height = st.W, st.H
			x, y = st.X, st.Y
			initPos = application.WindowXY
		}
		st.Kind, st.Category, st.Open = kind, category, true
		savePopoutStoreLocked()
	}
	popoutsMu.Unlock()

	// User-initiated pop-outs flash their title bar on mount (red fading to
	// normal) so the fresh — often empty — overlay is easy to locate over the
	// game. Startup/character-swap restores skip the flash.
	if focus {
		hash += "&flash=1"
	}

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
		switch {
		case popoutSwitchClosing[name]:
			// Closed by a character swap, not by the user — leave the owning
			// character's layout alone so it returns on their next login.
			delete(popoutSwitchClosing, name)
		case appQuitting:
			// A graceful quit keeps the open set intact so it reopens next launch.
		default:
			// A user closing the overlay marks it closed for its owner.
			var s *popoutState
			if kind == "map" {
				s = popoutMapSt
			} else if isSpecialPopoutKind(kind) {
				s = popoutSpecials[name]
			} else {
				s = popoutCharStateLocked(owner, kind, category, name, false)
			}
			if s != nil {
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

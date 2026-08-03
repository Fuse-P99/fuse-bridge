package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
//   - EVERY OTHER overlay (timers, alerts, and the special overlays) is per
//     character, so each toon keeps its own set, positions and sizes. A character
//     logging in for the first time is seeded (see seedLayoutForLocked): from the
//     user's DEFAULT layout if they've authored one, else from a configured
//     same-class character, else nothing — plus the migrated legacy
//     special-overlay records (see migrateSpecialsForLocked). Once seeded, a
//     character's own edits stay that character's; the default is a starting
//     point, never a live link.
//
// Per-overlay look settings (background colour/opacity, always-on-top) live in
// the frontend's localStorage, keyed by character for everything but the map.
// Per-character overlays are closed and reopened on a character swap, so each
// one re-reads the incoming character's settings when it mounts.
var (
	popoutsMu sync.Mutex
	popouts   = map[string]*application.WebviewWindow{} // live windows by name

	popoutMapSt         *popoutState                           // app-wide map overlay
	popoutSpecials      = map[string]*popoutState{}            // LEGACY app-wide special-overlay records (migration seed only)
	popoutChars         = map[string]map[string]*popoutState{} // lower(char) → window name → state
	popoutCharClass     = map[string]string{}                  // lower(char) → class, for same-class inheritance
	popoutActiveChar    string                                 // lower(char) whose timer overlays are showing
	popoutSwitchClosing = map[string]bool{}                    // windows being closed by a character switch
	popoutsLocked       bool                                   // Lock toggle, persisted; guarded by popoutsMu
	appQuitting         bool                                   // guarded by popoutsMu

	// Default-editing mode: popoutActiveChar points at popoutDefaultKey and the
	// overlays on screen ARE the default layout. Deliberately not persisted — an
	// app restart should come back playing a character, not editing defaults.
	// popoutPendingChar remembers who to switch back to, since the tailer keeps
	// reporting character changes while the mode is on.
	popoutDefaultEditing bool
	popoutPendingChar    string
)

// popoutSeedKey parks a pre-per-character layout until the first character logs
// in and adopts it, so upgrading doesn't discard an existing overlay setup.
const popoutSeedKey = "*"

// popoutDefaultKey holds the user's DEFAULT overlay layout: the starting point
// every character with no layout of its own inherits.
//
// It lives in popoutChars as a pseudo-character rather than in its own field on
// purpose. Editing the default then means nothing more than pointing
// popoutActiveChar at this key — every existing mechanism (pop-out, drag,
// SavePopoutState, the per-character localStorage suffix the overlays use for
// their look settings) keeps working with no special cases, because from their
// point of view the user simply switched characters.
//
// Unlike popoutSeedKey it is never consumed; seeding copies out of it.
const popoutDefaultKey = "*default*"

// isReservedPopoutKey reports whether a popoutChars key is one of the synthetic
// ones rather than a real character. They must be skipped anywhere characters
// are enumerated — inheriting your "default" as if it were a same-class
// guildmate's layout would be nonsense.
func isReservedPopoutKey(key string) bool {
	return key == popoutSeedKey || key == popoutDefaultKey
}

// isSpecialPopoutKind reports whether kind is one of the "Special Overlays" —
// fixed app-wide sections (live raid cards, voice speakers) popped out as
// overlays. One instance each, but — like the timer overlays — every character
// remembers their own geometry/open state; a swap closes the outgoing
// character's set and opens the incoming one's. (They were app-wide before;
// migrateSpecialsForLocked seeds each character from that legacy state.)
func isSpecialPopoutKind(kind string) bool {
	switch kind {
	case "raidassign", "raiddebuffs", "raidclerics", "othertimers", "voicespeakers", "randoms", "threat", "raiddps":
		return true
	}
	return false
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
// log goes quiet (auto), via the footer Hide/Restore Windows button (manual),
// and — when the option is on — while a window that is neither EverQuest nor
// this app is in the foreground (focus; see focuswatch.go). The reasons are
// OR'd — clearing one doesn't un-hide the others.
var (
	popoutVisMu         sync.Mutex
	popoutsManualHidden bool // Timers-window Hide toggle
	popoutsAutoHidden   bool // camp-out / long idle
	popoutsFocusHidden  bool // another app has the foreground window
)

func popoutsEffectiveHidden() bool {
	popoutVisMu.Lock()
	defer popoutVisMu.Unlock()
	return popoutsManualHidden || popoutsAutoHidden || popoutsFocusHidden
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
	if !hidden {
		// Field report: after an auto-hide the restore sometimes didn't take —
		// flags said visible but the windows stayed hidden until the manual
		// Hide/Restore toggle re-ran the exact same Show calls. Re-assert once
		// shortly after; applyPopoutVisibility recomputes state, so this is a
		// no-op unless something re-hid (or a Show was dropped) in between.
		time.AfterFunc(time.Second, applyPopoutVisibility)
	}
}

// setPopoutsFocusHidden latches the "another app is active" hide. Called from
// the focus watcher every poll tick, so it must stay idempotent — Show/Hide
// only run on an actual state change.
func setPopoutsFocusHidden(hidden bool) {
	popoutVisMu.Lock()
	if popoutsFocusHidden == hidden {
		popoutVisMu.Unlock()
		return
	}
	popoutsFocusHidden = hidden
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

// GetHideOverlaysUnfocused reports whether overlays auto-hide while a window
// other than EverQuest (or this app) is in the foreground.
func (a *App) GetHideOverlaysUnfocused() bool { return GetSettings().HideOverlaysUnfocused }

func (a *App) SetHideOverlaysUnfocused(on bool) {
	s := GetSettings()
	s.HideOverlaysUnfocused = on
	UpdateSettings(s)
	if !on {
		// Restore immediately rather than waiting for the watcher's next tick.
		setPopoutsFocusHidden(false)
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
	Locked bool         `json:"locked"`
	Map    *popoutState `json:"map,omitempty"`
	// Specials is the legacy app-wide special-overlay layout. Specials are per
	// character now (inside Chars); this is kept as the migration seed for
	// characters not played since the switch.
	Specials  map[string]*popoutState            `json:"specials,omitempty"`
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
	// Special overlays are per character like the timer overlays (the legacy
	// app-wide records seed each character's first use — see
	// migrateSpecialsForLocked).
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
		if k == key || isReservedPopoutKey(k) || len(set) == 0 {
			continue
		}
		if strings.EqualFold(popoutCharClass[k], cls) {
			return set
		}
	}
	return nil
}

// defaultLayoutLocked returns the configured default layout, or nil when the
// user hasn't made one. Caller holds popoutsMu.
func defaultLayoutLocked() map[string]*popoutState {
	if set := popoutChars[popoutDefaultKey]; len(set) > 0 {
		return set
	}
	return nil
}

// hasTimerLayoutLocked reports whether a character has any per-category timer
// or alert overlay records. Special overlays don't count: every character gets
// those via migrateSpecialsForLocked, so their presence alone must not mark a
// character "configured" and block same-class timer-layout inheritance.
// Caller holds popoutsMu.
func hasTimerLayoutLocked(key string) bool {
	for _, st := range popoutChars[key] {
		if st != nil && st.Kind != "map" && !isSpecialPopoutKind(st.Kind) {
			return true
		}
	}
	return false
}

// seedLayoutForLocked gives a first-time character a starting layout: a migrated
// pre-per-character setup if one is parked, else a same-class character's layout.
// Merges into any existing set (which may already hold migrated special
// overlays), never overwriting an entry. Returns true when one was adopted.
// Caller holds popoutsMu.
func seedLayoutForLocked(key, charName string) bool {
	if isReservedPopoutKey(key) {
		return false // the default layout is authored, never inherited
	}
	if hasTimerLayoutLocked(key) {
		return false // already configured
	}
	// Order matters: a parked pre-per-character layout is this install's own
	// history and must not be discarded, but after that an explicitly authored
	// default outranks guessing from a same-class character.
	donor := popoutChars[popoutSeedKey]
	if len(donor) > 0 {
		delete(popoutChars, popoutSeedKey)
	} else if d := defaultLayoutLocked(); d != nil {
		donor = d
	} else {
		donor = sameClassLayoutLocked(key, charName)
	}
	if len(donor) == 0 {
		return false
	}
	set := popoutChars[key]
	if set == nil {
		set = map[string]*popoutState{}
		popoutChars[key] = set
	}
	for n, st := range clonePopoutSet(donor) {
		if set[n] == nil {
			set[n] = st
		}
	}
	return true
}

// migrateSpecialsForLocked seeds a character's special-overlay records from the
// legacy app-wide set the first time that character becomes active, so the
// switch to per-character specials keeps the geometry/open state everyone
// already had. The legacy records stay stored as the seed for characters not
// played since the switch. Caller holds popoutsMu.
func migrateSpecialsForLocked(key string) {
	if key == "" || len(popoutSpecials) == 0 {
		return
	}
	set := popoutChars[key]
	if set == nil {
		set = map[string]*popoutState{}
		popoutChars[key] = set
	}
	for name, st := range popoutSpecials {
		if st == nil || set[name] != nil || !isSpecialPopoutKind(st.Kind) {
			continue
		}
		c := *st
		set[name] = &c
	}
}

// popoutIdent resolves a (kind, category) pair to a stable window name plus the
// window's title, URL hash, and default/min geometry.
func popoutIdent(kind, category string) (name, title, hash string, defW, defH, minW, minH int) {
	switch kind {
	// Special Overlays: one instance each, per-character geometry/open state.
	case "raidassign":
		return "popout-raidassign", "Raid Assignments", "#popout=raidassign", 320, 280, 220, 140
	case "raiddebuffs":
		return "popout-raiddebuffs", "Raid Debuffs", "#popout=raiddebuffs", 300, 240, 200, 120
	case "raidclerics":
		return "popout-raidclerics", "Raid Clerics", "#popout=raidclerics", 300, 280, 200, 140
	case "othertimers":
		// Kind stays "othertimers" though the display name is now "Raid Specific
		// Timers": the kind keys every character's saved overlay geometry and
		// open state, so renaming it would silently reset everyone's layout.
		return "popout-othertimers", "Raid Specific Timers", "#popout=othertimers", 320, 200, 200, 100
	case "voicespeakers":
		return "popout-voicespeakers", "Voice Speakers", "#popout=voicespeakers", 260, 160, 180, 80
	case "randoms":
		// Taller than the other specials: a roll-off is a ranked list that can
		// run the length of a raid, and the height fits to content anyway.
		return "popout-randoms", "Randoms", "#popout=randoms", 260, 300, 170, 100
	case "threat":
		return "popout-threat", "Threat Meter", "#popout=threat", 240, 300, 180, 200
	case "raiddps":
		// Two columns (top 5 / by class) side by side, so wider than the rest.
		return "popout-raiddps", "Raid DPS", "#popout=raiddps", 300, 220, 220, 120
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
			if k == char || isReservedPopoutKey(k) || len(set) == 0 {
				continue
			}
			if strings.EqualFold(popoutCharClass[k], cls) {
				donor = k
				break
			}
		}
	}
	// "defaults" is the look-settings counterpart of the geometry seeding in
	// seedLayoutForLocked, and is ranked the same way by the overlay: an
	// authored default beats a same-class guess. "editing" lets the overlay
	// badge its own title bar so it's obvious which set is being arranged.
	defaults := ""
	if defaultLayoutLocked() != nil {
		defaults = popoutDefaultKey
	}
	editing := ""
	if popoutDefaultEditing {
		editing = "1"
	}
	return map[string]string{
		"char": char, "donor": donor, "defaults": defaults, "editing": editing,
	}
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

// ── default overlay layout ──────────────────────────────────────────────────

// OverlayDefaultInfo is the Manage Overlays view's snapshot of the default
// layout: whether one exists, whether it's being edited right now, and which
// character the Reset button should name.
type OverlayDefaultInfo struct {
	Configured bool   `json:"configured"` // a default layout exists
	Editing    bool   `json:"editing"`    // default-editing mode is on
	Count      int    `json:"count"`      // overlays in the default layout
	Char       string `json:"char"`       // the character in play (display case)
	CharSet    bool   `json:"char_set"`   // that character has its own layout
	DefaultKey string `json:"default_key"`
}

// GetOverlayDefaultInfo describes the default layout for the UI. Bound.
func (a *App) GetOverlayDefaultInfo() OverlayDefaultInfo {
	popoutsMu.Lock()
	defer popoutsMu.Unlock()
	char := currentCharName
	if popoutDefaultEditing && popoutPendingChar != "" {
		char = popoutPendingChar
	}
	d := defaultLayoutLocked()
	return OverlayDefaultInfo{
		Configured: d != nil,
		Editing:    popoutDefaultEditing,
		Count:      len(d),
		Char:       char,
		CharSet:    char != "" && hasTimerLayoutLocked(strings.ToLower(char)),
		DefaultKey: popoutDefaultKey,
	}
}

// SetOverlayDefaultEditing enters or leaves default-editing mode. Entering
// swaps the on-screen overlays for the default set, so arranging them IS
// authoring the default; leaving swaps the playing character's set back. Bound.
func (a *App) SetOverlayDefaultEditing(on bool) {
	if !popoutsCanOpen() {
		return
	}
	popoutsMu.Lock()
	if popoutDefaultEditing == on {
		popoutsMu.Unlock()
		return
	}
	popoutDefaultEditing = on
	target := popoutDefaultKey
	if !on {
		// Back to whoever is being played — the tailer may have swapped
		// characters underneath us while the mode was on.
		target = popoutPendingChar
		if target == "" {
			target = strings.ToLower(currentCharName)
		}
		popoutPendingChar = ""
		if target == "" {
			// No character known yet: just close the default set rather than
			// leaving it on screen pretending to belong to someone.
			toClose, _ := switchPopoutSetLocked("")
			popoutsMu.Unlock()
			applyPopoutSwitch(toClose, nil)
			emitOverlayDefaults()
			return
		}
		seedLayoutForLocked(target, target)
		migrateSpecialsForLocked(target)
	} else {
		popoutPendingChar = strings.ToLower(currentCharName)
	}
	toClose, toOpen := switchPopoutSetLocked(target)
	popoutsMu.Unlock()

	applyPopoutSwitch(toClose, toOpen)
	if on {
		addStatus("Overlays: editing the DEFAULT layout — changes seed every new character")
	} else {
		addStatus("Overlays: back to %s's layout", target)
	}
	emitOverlayDefaults()
}

// SaveOverlayDefaultFromCharacter copies a character's current overlay layout
// over the default, so an existing setup can become the default without being
// rebuilt by hand. Bound.
func (a *App) SaveOverlayDefaultFromCharacter(charName string) error {
	key := strings.ToLower(strings.TrimSpace(charName))
	if key == "" || isReservedPopoutKey(key) {
		return fmt.Errorf("no character is being played")
	}
	popoutsMu.Lock()
	set := popoutChars[key]
	if len(set) == 0 {
		popoutsMu.Unlock()
		return fmt.Errorf("%s has no overlays set up yet", charName)
	}
	popoutChars[popoutDefaultKey] = clonePopoutSet(set)
	savePopoutStoreLocked()
	n := len(popoutChars[popoutDefaultKey])
	popoutsMu.Unlock()

	addStatus("Overlays: saved %s's layout (%d overlays) as the default", charName, n)
	emitOverlayDefaults()
	return nil
}

// ResetCharacterToDefault discards a character's own overlay layout and gives
// them a fresh copy of the default. The character's overlay LOOK settings live
// in the frontend's localStorage, so the caller mirrors this there — this half
// only owns geometry and the open set. Bound.
func (a *App) ResetCharacterToDefault(charName string) error {
	key := strings.ToLower(strings.TrimSpace(charName))
	if key == "" || isReservedPopoutKey(key) {
		return fmt.Errorf("no character is being played")
	}
	popoutsMu.Lock()
	d := defaultLayoutLocked()
	if d == nil {
		popoutsMu.Unlock()
		return fmt.Errorf("no default overlay layout has been set up yet")
	}
	popoutChars[key] = clonePopoutSet(d)
	// Reapply immediately when this is the set on screen; otherwise the records
	// are enough — the character's overlays open from them at their next login.
	var toClose []*application.WebviewWindow
	var toOpen []*popoutState
	live := popoutActiveChar == key
	if live {
		toClose, toOpen = switchPopoutSetLocked(key)
	} else {
		savePopoutStoreLocked()
	}
	popoutsMu.Unlock()

	if live {
		applyPopoutSwitch(toClose, toOpen)
	}
	addStatus("Overlays: reset %s to the default layout", charName)
	emitOverlayDefaults()
	return nil
}

// emitOverlayDefaults nudges the Manage Overlays view to re-read its state.
func emitOverlayDefaults() {
	if v3App != nil {
		v3App.Event.Emit("overlay-defaults-changed")
	}
}

// ReopenSavedPopouts reopens the app-wide map overlay if it was open. Every
// other overlay (timers, alerts, and the special overlays) is per character
// and is opened by ApplyPopoutsForCharacter once the tailed character is
// known. Called once the application is ready.
func ReopenSavedPopouts() {
	popoutsMu.Lock()
	open := popoutMapSt != nil && popoutMapSt.Open
	popoutsMu.Unlock()
	if open {
		openPopoutWindow("map", "", false) // don't steal focus from the game
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

	popoutsMu.Lock()
	// While the default layout is being edited, the overlays on screen belong to
	// the default — a character swap must not yank them away mid-arrangement.
	// Remember who to switch to when the mode ends.
	if popoutDefaultEditing {
		popoutPendingChar = key
		if cls := classForCharacter(charName); cls != "" {
			popoutCharClass[key] = cls
		}
		popoutsMu.Unlock()
		return
	}
	if popoutActiveChar == key {
		popoutsMu.Unlock()
		return
	}
	seedLayoutForLocked(key, charName)
	migrateSpecialsForLocked(key)
	if cls := classForCharacter(charName); cls != "" {
		popoutCharClass[key] = cls
	}
	toClose, toOpen := switchPopoutSetLocked(key)
	popoutsMu.Unlock()

	applyPopoutSwitch(toClose, toOpen)
}

// switchPopoutSetLocked makes key the active overlay set: it returns every live
// per-character overlay to close (the map is app-wide and stays) and the target
// set's records to open. Closes are flagged as switch-closes so the outgoing
// set's saved layout survives — those overlays must come back next time.
// Caller holds popoutsMu; the returned work happens after the unlock.
func switchPopoutSetLocked(key string) ([]*application.WebviewWindow, []*popoutState) {
	mapName, _, _, _, _, _, _ := popoutIdent("map", "")
	popoutActiveChar = key

	var toOpen []*popoutState
	for _, st := range popoutChars[key] {
		if st != nil && st.Open {
			toOpen = append(toOpen, st)
		}
	}
	var toClose []*application.WebviewWindow
	for n, w := range popouts {
		if n == mapName {
			continue
		}
		popoutSwitchClosing[n] = true
		delete(popouts, n)
		toClose = append(toClose, w)
	}
	savePopoutStoreLocked()
	return toClose, toOpen
}

// applyPopoutSwitch performs the window work switchPopoutSetLocked planned.
// Must run with popoutsMu released — closing and creating windows re-enters it.
func applyPopoutSwitch(toClose []*application.WebviewWindow, toOpen []*popoutState) {
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
	// Migrated special overlays alone don't make a character "configured" —
	// hasTimerLayoutLocked ignores them, so the timer-layout inheritance below
	// still runs for an alt whose set only holds specials.
	if key != popoutActiveChar || hasTimerLayoutLocked(key) {
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

// GetOpenPopouts returns the window names of every overlay currently open
// (popoutIdent names, e.g. "popout-timers-Buffs (Self)"). The trigger tree's
// popout shortcut buttons use it to show which overlays are already up.
func (a *App) GetOpenPopouts() []string {
	popoutsMu.Lock()
	defer popoutsMu.Unlock()
	out := make([]string, 0, len(popouts))
	for n := range popouts {
		out = append(out, n)
	}
	return out
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
		// Never un-hide behind the hide latch: overlays are hidden as a set,
		// and an automatic reopen (focus=false — startup, character switch)
		// must not resurrect one on its own. An explicit pop-out click is the
		// user asking for it, so that still shows.
		if focus || !popoutsEffectiveHidden() {
			w.Show()
		}
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
		// A record that has been through SavePopoutState always carries a real
		// size, so it carries a real position too — that, not the size passing
		// minW/minH, is what says "we know where this window goes".
		//
		// The old test gated POSITION on the size check, which shrink-to-content
		// broke: a fitted special overlay legitimately stores a height below its
		// minimum (an idle one is barely taller than its title bar), so the whole
		// branch was skipped and it reopened centered at default size. The map
		// never fits, which is why it was the only one still landing correctly.
		if st.W > 0 && st.H > 0 {
			x, y = st.X, st.Y
			initPos = application.WindowXY
			// Create at no less than the minimum; a fitting overlay shrinks
			// itself back down to its content as soon as it mounts.
			width, height = max(st.W, minW), max(st.H, minH)
		}
		st.Kind, st.Category, st.Open = kind, category, true
		savePopoutStoreLocked()
	}
	popoutsMu.Unlock()

	// Now that overlay positions are true screen coordinates, a saved one can
	// name a monitor that has since been unplugged. An overlay is frameless and
	// off the taskbar, so a stranded one can't be dragged back — there'd be
	// nothing to grab. Fall back to centering.
	if initPos == application.WindowXY && !rectVisibleOnScreen(x, y, width, height) {
		initPos = application.WindowCentered
		addStatus("Overlay %s was saved off-screen — centering it", title)
	}

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

	// Same rule as the already-open branch above: a window created while the
	// set is hidden (a character switch during a camp-out, the deferred
	// startup open) stays hidden until the set is restored. Without this it
	// came up visible and STAYED visible — the latch is idempotent, so the
	// next setPopoutsAutoHidden(true) is a no-op and never hides it.
	if focus || !popoutsEffectiveHidden() {
		w.Show()
	}
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

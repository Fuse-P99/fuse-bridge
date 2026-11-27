package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Characters tab quick results — "who has this, and where is it?" answered
// across every character at once.
//
// The detail pane already searches ONE character's dump. This is the other
// half, and the question actually asked before a raid ("does anyone have a
// Peggy Cloak?"): it walks every character the tab's filters leave visible and
// returns one row per copy found.
//
// One row per copy, never merged. Two stacks of Rations in different bags are
// two rows, because the whole point of the table is to say where to go get the
// thing — a merged "you have 20" would hide that they're in two places. Stack
// size goes in Count instead.

// ItemHit is one located copy of a matched item, or one known spell.
type ItemHit struct {
	Char string `json:"char"`
	Kind string `json:"kind"` // "item" | "spell"
	Name string `json:"name"` // the matched item/spell, for grouping
	// Where is the broad bucket: Equipped, Bags, Bank, Shared Bank, Spellbook.
	Where string `json:"where"`
	// Location is the exact slot — "Chest", "Bag 3, Slot 7", "Bank 2".
	// Empty for spells: the spellbook file's slot number is a page position
	// that shifts as spells are scribed, so it locates nothing.
	Location string `json:"location"`
	Count    int    `json:"count"`
	// Level is the spell's class level from the server's spell list. 0 means
	// unknown (offline, or the class never resolved), which the UI renders as
	// a bare "Spellbook" rather than inventing a number.
	Level int `json:"level"`
}

// CharSearchResult carries the truncation flag so the UI can say "showing 500
// of 2384" instead of silently lying about how much matched.
type CharSearchResult struct {
	Rows      []ItemHit `json:"rows"`
	Total     int       `json:"total"`
	Truncated bool      `json:"truncated"`
}

const (
	// charSearchMinQuery keeps a single keystroke from walking every
	// inventory file on disk to match essentially everything.
	charSearchMinQuery = 2
	// charSearchMaxRows caps what crosses the bridge. A two-letter query can
	// legitimately match thousands of rows; the table is for finding a thing,
	// not for reading all of them.
	charSearchMaxRows = 500
)

// reInvLoc splits an EQ inventory Location: a slot name, an optional container
// number, and an optional "-SlotN" position inside that container.
var reInvLoc = regexp.MustCompile(`^([A-Za-z]+?)(\d*)(?:-Slot(\d+))?$`)

// pairedSlots are worn slots EQ writes more than once per character. The file
// gives no index, so they're numbered in file order — the same convention the
// Magelo view uses, so the two agree about which ring is which.
var pairedSlots = map[string]bool{"Ear": true, "Wrist": true, "Fingers": true, "Finger": true}

// describeInvLocation turns one Location field into the table's two columns.
// Anything that isn't a recognised container is worn: the dump uses the bare
// slot name ("Chest", "Primary"), and an unknown name is far more likely to be
// a slot this function hasn't seen than a container.
func describeInvLocation(loc string, seen map[string]int) (where, detail string) {
	m := reInvLoc.FindStringSubmatch(loc)
	if m == nil {
		return "Equipped", loc
	}
	base, num, slot := m[1], m[2], m[3]

	label := ""
	switch {
	case strings.EqualFold(base, "SharedBank"):
		where, label = "Shared Bank", "Shared "+num
	case strings.EqualFold(base, "Bank"):
		where, label = "Bank", "Bank "+num
	case strings.EqualFold(base, "General"):
		// General1-8 are the eight inventory slots. The bag itself lives there;
		// its contents carry the -SlotN suffix.
		where, label = "Bags", "Bag "+num
	default:
		// Worn. Number the paired slots in file order so two earrings don't
		// both read "Ear" and look like a duplicate row.
		if pairedSlots[base] {
			seen[base]++
			return "Equipped", strings.TrimSuffix(base, "s") + " " + strconv.Itoa(seen[base])
		}
		return "Equipped", loc
	}
	if slot != "" {
		return where, label + ", Slot " + slot
	}
	return where, label
}

// whereRank orders the Where buckets from "on them right now" to "furthest
// away", which is also the order of how useful the answer is.
var whereRank = map[string]int{"Equipped": 0, "Bags": 1, "Bank": 2, "Shared Bank": 3, "Spellbook": 4}

// ── spell levels ─────────────────────────────────────────────────────────────

// Spell levels come from the server, one request per class. Characters share
// classes heavily, so this cache turns "every enchanter on the account" into a
// single fetch. Failure caches nothing: an offline lookup should retry on the
// next search, not poison the class for half an hour.
var (
	spellLevelMu    sync.Mutex
	spellLevelCache = map[string]map[string]int{} // lower(class) -> lower(spell) -> level
	spellLevelAt    = map[string]time.Time{}
)

const spellLevelTTL = 30 * time.Minute

func spellLevelsFor(class string) map[string]int {
	key := strings.ToLower(strings.TrimSpace(class))
	if key == "" {
		return nil
	}
	spellLevelMu.Lock()
	if at, ok := spellLevelAt[key]; ok && time.Since(at) < spellLevelTTL {
		m := spellLevelCache[key]
		spellLevelMu.Unlock()
		return m
	}
	spellLevelMu.Unlock()

	spells := fetchSpellsForClass(class)
	if len(spells) == 0 {
		return nil
	}
	m := make(map[string]int, len(spells))
	for _, s := range spells {
		m[normalizeItemName(s.Name)] = s.Level
	}
	spellLevelMu.Lock()
	spellLevelCache[key] = m
	spellLevelAt[key] = time.Now()
	spellLevelMu.Unlock()
	return m
}

// ── search ───────────────────────────────────────────────────────────────────

// SearchCharItems finds every copy of a matching item, and every matching
// scribed spell, across the characters the tab's filters leave visible.
//
// The filter arguments mirror the tab's checkboxes on purpose: results that
// included the bots you asked to hide would send you to a character that isn't
// yours to loot from.
func (a *App) SearchCharItems(query string, excludeBots, excludeFiltered bool) CharSearchResult {
	q := strings.ToLower(strings.TrimSpace(query))
	if len([]rune(q)) < charSearchMinQuery {
		return CharSearchResult{}
	}
	needle := normalizeItemName(q)

	eqDir := GetSettings().EQDirectory
	if eqDir == "" {
		return CharSearchResult{}
	}

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

	infos := cachedCharInfos(names)
	var rows []ItemHit

	for _, name := range names {
		// Items, in file order. describeInvLocation runs BEFORE the match
		// check on purpose: it counts the paired slots as it goes, so skipping
		// it for non-matching rows would number the second earring "Ear 1"
		// whenever the first one didn't match the query.
		seen := map[string]int{}
		for _, it := range readInventoryItems(name, eqDir) {
			where, detail := describeInvLocation(it.Location, seen)
			if !strings.Contains(normalizeItemName(it.Name), needle) {
				continue
			}
			rows = append(rows, ItemHit{
				Char: name, Kind: "item", Name: it.Name,
				Where: where, Location: detail, Count: it.Count,
			})
		}

		// Spells. Level is resolved lazily and only when this character
		// actually has a hit, so a search that matches no spells never touches
		// the network.
		var levels map[string]int
		levelsLoaded := false
		for _, sp := range a.GetCharSpellbook(name) {
			if !strings.Contains(normalizeItemName(sp), needle) {
				continue
			}
			if !levelsLoaded {
				levels = spellLevelsFor(infos[strings.ToLower(name)].Class)
				levelsLoaded = true
			}
			rows = append(rows, ItemHit{
				Char: name, Kind: "spell", Name: sp,
				Where: "Spellbook", Level: levels[normalizeItemName(sp)],
			})
		}
	}

	// Grouped by what was found, then by who has it — the table renders one
	// header per distinct name, and this is the order those headers appear in.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ai, bi := strings.ToLower(a.Name), strings.ToLower(b.Name); ai != bi {
			return ai < bi
		}
		if a.Char != b.Char {
			return strings.ToLower(a.Char) < strings.ToLower(b.Char)
		}
		if wa, wb := whereRank[a.Where], whereRank[b.Where]; wa != wb {
			return wa < wb
		}
		return a.Location < b.Location
	})

	total := len(rows)
	if len(rows) > charSearchMaxRows {
		rows = rows[:charSearchMaxRows]
	}
	return CharSearchResult{Rows: rows, Total: total, Truncated: total > len(rows)}
}

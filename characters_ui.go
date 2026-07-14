package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// CharInfo is a character's level, class, race, and last-seen zone for display.
type CharInfo struct {
	Level int    `json:"level"`
	Class string `json:"class"`
	Zone  string `json:"zone"`
	Race  string `json:"race"`
}

// fetchCharInfos returns level+class for the given character names (keyed by
// lowercased name). Names with no server-side data are omitted.
func fetchCharInfos(names []string) map[string]CharInfo {
	out := map[string]CharInfo{}
	if len(names) == 0 {
		return out
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string][]string{"names": names})
	req, err := http.NewRequest(http.MethodPost, base+"/charinfos", bytes.NewReader(body))
	if err != nil {
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	var r struct {
		Infos map[string]CharInfo `json:"infos"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) == nil && r.Infos != nil {
		return r.Infos
	}
	return out
}

// botToons holds the lowercase names of toons belonging to the fusebot member.
// botToonsAttempt is the last time we tried to fetch the list (success or fail),
// used to throttle refreshes.
var (
	botToonsMu      sync.RWMutex
	botToons        = make(map[string]bool)
	botToonsAttempt time.Time
)

// botToonsRefreshInterval is the minimum spacing between bot-list fetches.
const botToonsRefreshInterval = time.Hour

// IsBotToon reports whether name is a fusebot-owned toon (case-insensitive).
func IsBotToon(name string) bool {
	botToonsMu.RLock()
	defer botToonsMu.RUnlock()
	return botToons[strings.ToLower(name)]
}

// maybeRefreshBotToons keeps the bot filter's list current without the app
// having to restart. The startup fetch is a single shot fired seconds after
// boot (main.go), so a transient failure then — the network not being up yet,
// a server blip, an auth token not yet provisioned — used to leave the list
// empty (or stale) for the entire session, silently disabling the "Exclude
// Bots" filter. The only bots that surface for a given user are ones they've
// personally logged in (those create the local log file the list is built
// from), so this presents as "a few bots I recently logged in aren't filtered."
//
// Called from GetCharNames so the Characters tab self-heals the moment it's
// viewed. Refreshes at most once per interval. Runs synchronously only when we
// have no data yet (the failed-startup case), so the first view after a failed
// boot fetch is correct; once we have a list, refreshes happen in the background
// and never block the UI. On failure the existing list is kept, not wiped.
func maybeRefreshBotToons() {
	botToonsMu.RLock()
	empty := len(botToons) == 0
	attempt := botToonsAttempt
	botToonsMu.RUnlock()

	if time.Since(attempt) < botToonsRefreshInterval {
		return
	}
	if empty {
		fetchBotToons()
	} else {
		go fetchBotToons()
	}
}

// fetchBotToons retrieves the list of fusebot toons from the server and replaces
// botToons with the fresh set. The attempt time is recorded up front so a failed
// fetch is still throttled (and doesn't hammer a down server on every keystroke).
// On any failure the previous list is left intact rather than cleared.
func fetchBotToons() {
	botToonsMu.Lock()
	botToonsAttempt = time.Now()
	botToonsMu.Unlock()

	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/bottoons", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		addStatus("Bot toons fetch error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var result struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}
	fresh := make(map[string]bool, len(result.Names))
	for _, n := range result.Names {
		fresh[strings.ToLower(n)] = true
	}
	botToonsMu.Lock()
	botToons = fresh
	botToonsMu.Unlock()
	addStatus("Retreived %d bot toon(s) for bot filter.", len(result.Names))
}

// getAllCharNames returns the union of character names known from the zone cache
// and EQ log files under eqDir/Logs, sorted alphabetically. Case-insensitive
// dedup preserves the first-seen capitalisation.
func getAllCharNames(eqDir string) []string {
	seen := make(map[string]bool) // lower → true
	var names []string

	for name := range GetAllZones() {
		if name != "" && !seen[strings.ToLower(name)] {
			seen[strings.ToLower(name)] = true
			names = append(names, name)
		}
	}

	for _, logsDir := range logsDirCandidates(eqDir) {
		entries, _ := os.ReadDir(logsDir)
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, "eqlog_") || !strings.HasSuffix(n, ".txt") {
				continue
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(n, "eqlog_"), ".txt")
			parts := strings.SplitN(inner, "_", 2)
			if len(parts) == 0 || parts[0] == "" {
				continue
			}
			charName := parts[0]
			if !seen[strings.ToLower(charName)] {
				seen[strings.ToLower(charName)] = true
				names = append(names, charName)
			}
		}
	}

	slices.Sort(names)
	return names
}

// fileModHeader returns a one-line header describing a file's modification time,
// e.g. "6/20/2026 - 3 days ago", for display above file content sections.
func fileModHeader(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	mod := info.ModTime()
	days := int(time.Since(mod).Hours() / 24)
	var ago string
	switch days {
	case 0:
		ago = "today"
	case 1:
		ago = "1 day ago"
	default:
		ago = fmt.Sprintf("%d days ago", days)
	}
	return fmt.Sprintf("%d/%d/%d - %s", mod.Month(), mod.Day(), mod.Year(), ago)
}

// updatedLine renders an "Updated: <ts> (<ago>)\r\n" line for a timestamp,
// used for both the last-seen zone and the bind point.
func updatedLine(t time.Time) string {
	elapsed := time.Since(t)
	ts := t.Format("2006-01-02 15:04:05")
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("Updated: %s (just now)\r\n", ts)
	case elapsed < time.Hour:
		return fmt.Sprintf("Updated: %s (%d minutes ago)\r\n", ts, int(elapsed.Minutes()))
	default:
		return fmt.Sprintf("Updated: %s (%d hours ago)\r\n", ts, int(elapsed.Hours()))
	}
}

// buildCharContent assembles the full right-pane text for a character: location
// block followed by inventory and spellbook file contents if they exist.
func buildCharContent(name, eqDir string) string {
	zones := GetAllZones()
	entry := zones[name]

	bind := GetAllBinds()[name]

	var sb strings.Builder

	// Location
	sb.WriteString("Location\r\n")
	sb.WriteString(strings.Repeat("-", 8) + "\r\n")

	// Last seen (current zone, from "You have entered" / /who).
	if entry.Zone != "" {
		fmt.Fprintf(&sb, "Last seen: %s\r\n", entry.Zone)
		if !entry.UpdatedAt.IsZero() {
			sb.WriteString(updatedLine(entry.UpdatedAt))
		}
	} else {
		sb.WriteString("Last seen: Unknown\r\n")
	}

	// Bind point (from "/char").
	if bind.Zone != "" {
		fmt.Fprintf(&sb, "Bind point: %s\r\n", bind.Zone)
		if !bind.UpdatedAt.IsZero() {
			sb.WriteString(updatedLine(bind.UpdatedAt))
		}
	} else {
		sb.WriteString("Bind point: Unknown\r\n")
		sb.WriteString("Run /char in game to update the bind point for this character.\r\n")
	}

	if eqDir == "" {
		return strings.TrimRight(sb.String(), "\r\n")
	}

	// Inventory — EQ writes CHARNAME-Inventory.txt in the install root
	// (or its VirtualStore mirror on Program Files installs).
	invPath := eqRootFilePath(eqDir, name+"-Inventory.txt")
	if data, err := os.ReadFile(invPath); invPath != "" && err == nil {
		sb.WriteString("\r\n")
		sb.WriteString("Inventory\r\n")
		sb.WriteString(strings.Repeat("-", 9) + "\r\n")
		if hdr := fileModHeader(invPath); hdr != "" {
			sb.WriteString(hdr + "\r\n")
		}
		content := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n", "\r\n")
		sb.WriteString(strings.TrimRight(content, "\r\n"))
		sb.WriteString("\r\n")
	}

	// Spellbook — EQ writes CHARNAME-Spellbook.txt in the install root
	// (or its VirtualStore mirror on Program Files installs).
	spellPath := eqRootFilePath(eqDir, name+"-Spellbook.txt")
	if data, err := os.ReadFile(spellPath); spellPath != "" && err == nil {
		sb.WriteString("\r\n")
		sb.WriteString("Spellbook\r\n")
		sb.WriteString(strings.Repeat("-", 9) + "\r\n")
		if hdr := fileModHeader(spellPath); hdr != "" {
			sb.WriteString(hdr + "\r\n")
		}
		content := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n", "\r\n")
		sb.WriteString(strings.TrimRight(content, "\r\n"))
		sb.WriteString("\r\n")
	}

	return strings.TrimRight(sb.String(), "\r\n")
}

// allMatches returns the byte offsets of every case-insensitive occurrence of
// query in content.
func allMatches(content, query string) []int {
	if query == "" {
		return nil
	}
	lower := strings.ToLower(content)
	lowerQ := strings.ToLower(query)
	var offsets []int
	for start := 0; ; {
		pos := strings.Index(lower[start:], lowerQ)
		if pos < 0 {
			break
		}
		offsets = append(offsets, start+pos)
		start += pos + len(lowerQ)
	}
	return offsets
}

// --- Filtered toons ---

var (
	filteredToonsMu sync.RWMutex
	filteredToons   = make(map[string]bool) // lower-cased names
)

func filteredToonsPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "filtered.json")
}

func loadFilteredToons() {
	data, err := os.ReadFile(filteredToonsPath())
	if err != nil {
		return
	}
	var names []string
	if json.Unmarshal(data, &names) == nil {
		filteredToonsMu.Lock()
		for _, n := range names {
			filteredToons[n] = true
		}
		filteredToonsMu.Unlock()
	}
}

func saveFilteredToons() {
	filteredToonsMu.RLock()
	names := make([]string, 0, len(filteredToons))
	for n := range filteredToons {
		names = append(names, n)
	}
	filteredToonsMu.RUnlock()
	slices.Sort(names)
	data, _ := json.Marshal(names)
	path := filteredToonsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

func IsFilteredToon(name string) bool {
	filteredToonsMu.RLock()
	defer filteredToonsMu.RUnlock()
	return filteredToons[strings.ToLower(name)]
}

func ToggleFilteredToon(name string) {
	lower := strings.ToLower(name)
	filteredToonsMu.Lock()
	if filteredToons[lower] {
		delete(filteredToons, lower)
	} else {
		filteredToons[lower] = true
	}
	filteredToonsMu.Unlock()
	saveFilteredToons()
}

// SetFilteredToons filters (filtered=true) or unfilters (false) many toons at
// once, persisting a single time — used by the Characters tab's multi-select
// "Filter All" / "Unfilter All". A per-toon ToggleFilteredToon loop would
// rewrite the filtered.json file once per name (hundreds of writes for players
// who manage 600+ toons).
func SetFilteredToons(names []string, filtered bool) {
	if len(names) == 0 {
		return
	}
	filteredToonsMu.Lock()
	for _, n := range names {
		lower := strings.ToLower(n)
		if filtered {
			filteredToons[lower] = true
		} else {
			delete(filteredToons, lower)
		}
	}
	filteredToonsMu.Unlock()
	saveFilteredToons()
}

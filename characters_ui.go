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

// CharInfo is a character's level, class, and last-seen zone for list display.
type CharInfo struct {
	Level int    `json:"level"`
	Class string `json:"class"`
	Zone  string `json:"zone"`
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
var (
	botToonsMu sync.RWMutex
	botToons   = make(map[string]bool)
)

// IsBotToon reports whether name is a fusebot-owned toon (case-insensitive).
func IsBotToon(name string) bool {
	botToonsMu.RLock()
	defer botToonsMu.RUnlock()
	return botToons[strings.ToLower(name)]
}

// fetchBotToons retrieves the list of fusebot toons from the server and
// populates botToons. Called once on startup.
func fetchBotToons() {
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
	botToonsMu.Lock()
	for _, n := range result.Names {
		botToons[strings.ToLower(n)] = true
	}
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

	if eqDir != "" {
		entries, _ := os.ReadDir(filepath.Join(eqDir, "Logs"))
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

// buildCharContent assembles the full right-pane text for a character: location
// block followed by inventory and spellbook file contents if they exist.
func buildCharContent(name, eqDir string) string {
	zones := GetAllZones()
	entry := zones[name]

	var sb strings.Builder

	// Location
	sb.WriteString("Location\r\n")
	sb.WriteString(strings.Repeat("-", 8) + "\r\n")
	if entry.Zone != "" {
		sb.WriteString(entry.Zone + "\r\n")
		if !entry.UpdatedAt.IsZero() {
			elapsed := time.Since(entry.UpdatedAt)
			ts := entry.UpdatedAt.Format("2006-01-02 15:04:05")
			switch {
			case elapsed < time.Minute:
				fmt.Fprintf(&sb, "Updated: %s (just now)\r\n", ts)
			case elapsed < time.Hour:
				fmt.Fprintf(&sb, "Updated: %s (%d minutes ago)\r\n", ts, int(elapsed.Minutes()))
			default:
				fmt.Fprintf(&sb, "Updated: %s (%d hours ago)\r\n", ts, int(elapsed.Hours()))
			}
		}
	} else {
		sb.WriteString("Unknown\r\n")
	}

	if eqDir == "" {
		return strings.TrimRight(sb.String(), "\r\n")
	}

	// Inventory — EQ writes CHARNAME-Inventory.txt in the install root.
	invPath := filepath.Join(eqDir, name+"-Inventory.txt")
	if data, err := os.ReadFile(invPath); err == nil {
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

	// Spellbook — EQ writes CHARNAME-Spellbook.txt in the install root.
	spellPath := filepath.Join(eqDir, name+"-Spellbook.txt")
	if data, err := os.ReadFile(spellPath); err == nil {
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

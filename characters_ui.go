package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

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

// searchInContent returns the byte offset of the first case-insensitive
// occurrence of query in content, or -1 if not found.
func searchInContent(content, query string) int {
	if query == "" {
		return -1
	}
	return strings.Index(strings.ToLower(content), strings.ToLower(query))
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type zoneChar struct {
	Name  string `json:"name"`
	Level int    `json:"level"`
	Class string `json:"class"`
	Race  string `json:"race"`
	Guild string `json:"guild"`
}

type zoneData struct {
	Name       string     `json:"name"`
	LastSeen   time.Time  `json:"last_seen"`
	Characters []zoneChar `json:"characters"`
}

type zonesResponse struct {
	Zones []zoneData `json:"zones"`
}

func fetchZoneSnoop() ([]zoneData, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/whozones", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var zr zonesResponse
	if err := json.NewDecoder(resp.Body).Decode(&zr); err != nil {
		return nil, err
	}
	return zr.Zones, nil
}

// fetchToonIdentities returns a map of lowercased toon name → Discord identity,
// used by the Zones tab to label Fuse members.
func fetchToonIdentities() (map[string]string, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/toonidentities", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var r struct {
		Identities map[string]string `json:"identities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return r.Identities, nil
}

func buildZoneContent(zone zoneData) string {
	type gEntry struct {
		total   int
		classes map[string]int
	}
	guildMap := make(map[string]*gEntry)
	for _, c := range zone.Characters {
		g := c.Guild
		if g == "" {
			g = "(No Guild)"
		}
		if guildMap[g] == nil {
			guildMap[g] = &gEntry{classes: make(map[string]int)}
		}
		guildMap[g].total++
		guildMap[g].classes[c.Class]++
	}

	// Fuse first, then remaining guilds sorted alphabetically.
	others := make([]string, 0, len(guildMap))
	hasFuse := false
	for g := range guildMap {
		if g == "Fuse" {
			hasFuse = true
		} else {
			others = append(others, g)
		}
	}
	sort.Strings(others)
	guildOrder := others
	if hasFuse {
		guildOrder = append([]string{"Fuse"}, others...)
	}

	var sb strings.Builder
	minutes := int(time.Since(zone.LastSeen).Minutes())
	header := fmt.Sprintf("%s (%d)  —  Seen %d minutes ago", zone.Name, len(zone.Characters), minutes)
	sb.WriteString(header + "\r\n")
	sb.WriteString(strings.Repeat("-", len(header)) + "\r\n\r\n")
	for _, g := range guildOrder {
		e := guildMap[g]
		sb.WriteString(fmt.Sprintf("<%s> (%d)\r\n", g, e.total))
		classes := make([]string, 0, len(e.classes))
		for c := range e.classes {
			classes = append(classes, c)
		}
		sort.Strings(classes)
		for _, c := range classes {
			sb.WriteString(fmt.Sprintf("  %s - %d\r\n", c, e.classes[c]))
		}
	}

	sb.WriteString("\r\n")
	sb.WriteString(strings.Repeat("-", 40) + "\r\n")
	sb.WriteString("\r\n")

	for _, c := range zone.Characters {
		guild := ""
		if c.Guild != "" {
			guild = " <" + c.Guild + ">"
		}
		race := ""
		if c.Race != "" {
			race = " (" + c.Race + ")"
		}
		if c.Class == "Anon" || c.Class == "Role" {
			sb.WriteString(fmt.Sprintf("[%s] %s%s%s\r\n", c.Class, c.Name, race, guild))
		} else {
			sb.WriteString(fmt.Sprintf("[%d %s] %s%s%s\r\n", c.Level, c.Class, c.Name, race, guild))
		}
	}
	return strings.TrimRight(sb.String(), "\r\n")
}

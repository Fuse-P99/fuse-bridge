package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type adminClientEntry struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`
	Toon     string    `json:"toon"`
	Guild    string    `json:"guild"`
	LastZone string    `json:"last_zone"`
	Version  string    `json:"version"`
	LastSeen time.Time `json:"last_seen"`
	Status   string    `json:"status"` // "active" | "connected" | "offline"
	Muted    bool      `json:"muted"`
}

// muteClient toggles server-side muting of a client row (drops all its data).
func muteClient(id int, muted bool) error {
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string]any{"id": id, "muted": muted})
	req, err := http.NewRequest(http.MethodPost, base+"/clients/mute", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

// SpellClassEntry mirrors the server's adminSpellClass (one class → min level).
type SpellClassEntry struct {
	Class string `json:"class"`
	Level int    `json:"level"`
}

// SpellPayload mirrors the server's adminSpellPayload — the reviewable spell
// record for the admin "Add missing spell" flow.
type SpellPayload struct {
	Name         string            `json:"name"`
	WikiURL      string            `json:"wiki_url"`
	Description  string            `json:"description"`
	SpellType    string            `json:"spell_type"`
	Mana         int               `json:"mana"`
	CastTime     string            `json:"cast_time"`
	RecoveryTime string            `json:"recovery_time"`
	RecastTime   string            `json:"recast_time"`
	SpellRange   string            `json:"spell_range"`
	AoERange     string            `json:"aoe_range"`
	Duration     string            `json:"duration"`
	ResistType   string            `json:"resist_type"`
	CastOnYou    string            `json:"cast_on_you"`
	CastOnOther  string            `json:"cast_on_other"`
	WearsOff     string            `json:"wears_off"`
	Classes      []SpellClassEntry `json:"classes"`
}

// scrapeSpellPreview asks the server to scrape a wiki spell URL and return the
// parsed fields (nothing is written yet).
func scrapeSpellPreview(spellURL string) (SpellPayload, error) {
	var out SpellPayload
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string]string{"url": spellURL})
	req, err := http.NewRequest(http.MethodPost, base+"/admin/spell/scrape", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	var payload struct {
		Spell SpellPayload `json:"spell"`
		Error string       `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return out, fmt.Errorf("unexpected server response")
	}
	if payload.Error != "" {
		return out, fmt.Errorf("%s", payload.Error)
	}
	return payload.Spell, nil
}

// addSpell writes a reviewed spell to the server DB.
func addSpell(p SpellPayload) error {
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(p)
	req, err := http.NewRequest(http.MethodPost, base+"/admin/spell/add", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("unexpected server response")
	}
	if payload.Error != "" {
		return fmt.Errorf("%s", payload.Error)
	}
	if !payload.OK {
		return fmt.Errorf("save failed")
	}
	return nil
}

// fetchIsOfficer asks the server whether this linked client's member holds the
// officer role. Any failure (unlinked, network, error) is treated as "not an
// officer" so the Clients tab simply stays hidden.
func fetchIsOfficer() bool {
	if !IsLinked() {
		return false
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/isofficer", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out struct {
		Officer bool `json:"officer"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Officer
}

// fetchIsAdmin asks the server whether this linked client's member holds the
// admin role pair (Officer + Bot Manager Discord roles). Any failure
// (unlinked, network, error) reads as "not admin" — admin UI simply stays
// hidden. The server re-checks on every admin endpoint regardless.
func fetchIsAdmin() bool {
	if !IsLinked() {
		return false
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/isadmin", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out struct {
		Admin bool `json:"admin"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Admin
}

func fetchClients() ([]adminClientEntry, string, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/clients", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var payload struct {
		Clients []adminClientEntry `json:"clients"`
		// LatestVersion is the server's current client build — the dashboard
		// derives its version gradations from it.
		LatestVersion string `json:"latest_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	return payload.Clients, payload.LatestVersion, nil
}

// GuildChatFeedLine is one overheard line in another guild's chat.
type GuildChatFeedLine struct {
	AtMs int64  `json:"at_ms"`
	Toon string `json:"toon"`
	Line string `json:"line"`
}

// GuildChatFeed is one non-Fuse guild's recent chat, oldest line first.
type GuildChatFeed struct {
	Guild string              `json:"guild"`
	Lines []GuildChatFeedLine `json:"lines"`
}

// fetchOtherGuildChat pulls the officer-level other-guild chat feeds. A 403
// (caller isn't an officer or admin) comes back as an empty list, not an
// error — the section simply doesn't render.
func fetchOtherGuildChat() ([]GuildChatFeed, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/clients/guildchat", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return []GuildChatFeed{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var payload struct {
		Feeds []GuildChatFeed `json:"feeds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Feeds == nil {
		payload.Feeds = []GuildChatFeed{}
	}
	return payload.Feeds, nil
}

// CrossGuildToon is one character on a client install whose latest /who
// sighting places it in a non-Fuse guild.
type CrossGuildToon struct {
	Client      string `json:"client"`
	Toon        string `json:"toon"`
	Guild       string `json:"guild"`
	GuildSeenMs int64  `json:"guild_seen_ms"`
	ReportedMs  int64  `json:"reported_ms"`
}

// fetchCrossGuildToons pulls the officer-level cross-guild character report.
// 403 (caller isn't an officer or admin) is an empty report, not an error.
func fetchCrossGuildToons() ([]CrossGuildToon, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/clients/crossguild", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return []CrossGuildToon{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var payload struct {
		Rows []CrossGuildToon `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Rows == nil {
		payload.Rows = []CrossGuildToon{}
	}
	return payload.Rows, nil
}

func fetchClientActivity() ([]string, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/clientactivity", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var payload struct {
		Lines []string `json:"lines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Lines, nil
}

func buildClientsText(clients []adminClientEntry) string {
	if len(clients) == 0 {
		return "No clients registered."
	}
	var sb strings.Builder
	for _, c := range clients {
		check := "[ ] "
		switch c.Status {
		case "active":
			check = "[✓] "
		case "connected":
			check = "[~] "
		}
		sb.WriteString(fmt.Sprintf("%s%-22s  %-10s  %s\r\n",
			check, c.Name, c.Version, relativeTime(c.LastSeen)))
	}
	return sb.String()
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

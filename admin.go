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

func fetchClients() ([]adminClientEntry, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/clients", nil)
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
		Clients []adminClientEntry `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Clients, nil
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

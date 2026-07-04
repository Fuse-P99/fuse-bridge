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

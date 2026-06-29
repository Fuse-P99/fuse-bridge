package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type adminClientEntry struct {
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	LastSeen time.Time `json:"last_seen"`
	Status   string    `json:"status"` // "active" | "connected" | "offline"
}

func fetchClients() ([]adminClientEntry, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/clients", nil)
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
	var payload struct {
		Clients []adminClientEntry `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Clients, nil
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

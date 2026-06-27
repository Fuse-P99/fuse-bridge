package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type mapLocPayload struct {
	Toon    string  `json:"toon"`
	Zone    string  `json:"zone"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Z       float64 `json:"z"`
	Heading float64 `json:"heading"`
}

// MapPosition is another guild member's position as returned by /maplocs.
type MapPosition struct {
	Name    string  `json:"name"`
	Zone    string  `json:"zone"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Z       float64 `json:"z"`
	Heading float64 `json:"heading"`
}

var (
	mapLocMu   sync.Mutex
	mapLocLast time.Time
)

const mapLocMinInterval = 1 * time.Second

// SendMapLoc posts the player's current position to the server's /maploc endpoint,
// throttled to at most once per second. Fire-and-forget: positions are ephemeral
// so failures are ignored.
func (s *Sender) SendMapLoc(toon string, pos PlayerPosition) {
	if toon == "" || pos.Zone == "" {
		return
	}
	mapLocMu.Lock()
	if time.Since(mapLocLast) < mapLocMinInterval {
		mapLocMu.Unlock()
		return
	}
	mapLocLast = time.Now()
	mapLocMu.Unlock()

	go func() {
		base := strings.TrimSuffix(s.serverURL, "/submit")
		body, _ := json.Marshal(mapLocPayload{
			Toon: toon, Zone: pos.Zone,
			X: pos.X, Y: pos.Y, Z: pos.Z, Heading: pos.Heading,
		})
		req, err := http.NewRequest(http.MethodPost, base+"/maploc", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		resp, err := s.client.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()
}

// ZoneNick pairs a zone's long name with its nicknames (mirrors the server type).
type ZoneNick struct {
	Name  string   `json:"name"`
	Nicks []string `json:"nicks"`
}

// fetchZoneInfo returns every zone's long name + nicknames for map resolution.
func fetchZoneInfo() ([]ZoneNick, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/zoneinfo", nil)
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
		Zones []ZoneNick `json:"zones"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return r.Zones, nil
}

// fetchMapPositions returns the live positions of guild members in the given zone.
func fetchMapPositions(zone string) ([]MapPosition, error) {
	if zone == "" {
		return nil, nil
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/maplocs?zone="+url.QueryEscape(zone), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var r struct {
		Positions []MapPosition `json:"positions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return r.Positions, nil
}

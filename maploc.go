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
		req.Header.Set("Authorization", authHeader())
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

// The zone display name EQ prints on "You have entered <name>." can differ from
// the name shown in a /who footer for the same zone (e.g. "the Wakening Lands"
// vs "Wakening Lands"). The server keys positions by the canonical long name, so
// the client must translate whatever it parsed to that canonical name via the
// eqzones data (long name + every nickname) before sending, or the /loc is
// dropped as an unknown zone. Without this, positions only flowed after a /who,
// whose footer name happened to match eqzones exactly.
var (
	zoneIdxMu sync.RWMutex
	zoneIdx   map[string]string // normalized name/nick -> canonical long name
	zoneIdxAt time.Time
)

const zoneIdxTTL = 30 * time.Minute

// normalizeZoneKey collapses a zone display name to a comparison key, mirroring
// the frontend's normalizeZone (lowercase, drop a leading "the ", strip every
// non-alphanumeric). This makes "the Wakening Lands.", "Wakening Lands", and
// "wakening lands" all compare equal.
func normalizeZoneKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "the ")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ensureZoneIndex lazily loads (and periodically refreshes) the eqzones name
// index from the server's /zoneinfo. A failed load leaves the previous index in
// place and is retried on the next call.
func ensureZoneIndex() {
	zoneIdxMu.RLock()
	fresh := zoneIdx != nil && time.Since(zoneIdxAt) < zoneIdxTTL
	zoneIdxMu.RUnlock()
	if fresh {
		return
	}
	zones, err := fetchZoneInfo()
	if err != nil || len(zones) == 0 {
		return
	}
	idx := make(map[string]string, len(zones)*3)
	for _, z := range zones {
		if z.Name == "" {
			continue
		}
		if k := normalizeZoneKey(z.Name); k != "" {
			idx[k] = z.Name
		}
		for _, n := range z.Nicks {
			if k := normalizeZoneKey(n); k != "" {
				idx[k] = z.Name
			}
		}
	}
	zoneIdxMu.Lock()
	zoneIdx = idx
	zoneIdxAt = time.Now()
	zoneIdxMu.Unlock()
}

// ResolveZoneName translates a raw zone display name (from "You have entered X."
// or a /who footer) to its canonical long name via the eqzones data. Returns ""
// when the name matches no known zone or nickname.
func ResolveZoneName(raw string) string {
	key := normalizeZoneKey(raw)
	if key == "" {
		return ""
	}
	ensureZoneIndex()
	zoneIdxMu.RLock()
	defer zoneIdxMu.RUnlock()
	return zoneIdx[key]
}

// canonicalZone returns the canonical long name for a parsed zone display name,
// falling back to the raw name when it matches no known zone so an unmapped zone
// is never silently discarded (the frontend can still attempt a map match).
func canonicalZone(raw string) string {
	if canon := ResolveZoneName(raw); canon != "" {
		return canon
	}
	return raw
}

// fetchZoneInfo returns every zone's long name + nicknames for map resolution.
func fetchZoneInfo() ([]ZoneNick, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/zoneinfo", nil)
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
	req.Header.Set("Authorization", authHeader())
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

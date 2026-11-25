package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Fuse Shared Markers: officer-curated markers every client renders as gold
// flags on the zone map. Reads are open (unlinked clients see them too);
// creating/deleting is officer-only and enforced server-side.

// FuseMarker is one shared marker as served by /fusemarkers.
type FuseMarker struct {
	ID   int     `json:"id"`
	Zone string  `json:"zone"`
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
}

// Per-zone cache so tab switches and redraw-triggered refetches don't hammer
// the server; the map's own refresh cadence picks up officer edits within a
// minute either way.
const fuseMarkerTTL = 60 * time.Second

type fuseMarkerCacheEntry struct {
	markers []FuseMarker
	at      time.Time
}

var (
	fuseMarkerMu    sync.Mutex
	fuseMarkerCache = map[string]fuseMarkerCacheEntry{}
)

func invalidateFuseMarkers(zone string) {
	fuseMarkerMu.Lock()
	delete(fuseMarkerCache, strings.ToLower(strings.TrimSpace(zone)))
	fuseMarkerMu.Unlock()
}

// GetFuseMarkers returns the shared markers for a zone (cached ~60s). Errors
// degrade to an empty list — the map keeps working offline.
func (a *App) GetFuseMarkers(zone string) []FuseMarker {
	key := strings.ToLower(strings.TrimSpace(zone))
	if key == "" {
		return []FuseMarker{}
	}
	fuseMarkerMu.Lock()
	if e, ok := fuseMarkerCache[key]; ok && time.Since(e.at) < fuseMarkerTTL {
		out := make([]FuseMarker, len(e.markers))
		copy(out, e.markers)
		fuseMarkerMu.Unlock()
		return out
	}
	fuseMarkerMu.Unlock()

	u := registerBase() + "/fusemarkers?zone=" + url.QueryEscape(key)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(u)
	if err != nil {
		return []FuseMarker{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return []FuseMarker{}
	}
	var out struct {
		Markers []FuseMarker `json:"markers"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return []FuseMarker{}
	}
	if out.Markers == nil {
		out.Markers = []FuseMarker{}
	}
	fuseMarkerMu.Lock()
	fuseMarkerCache[key] = fuseMarkerCacheEntry{markers: out.Markers, at: time.Now()}
	fuseMarkerMu.Unlock()
	return out.Markers
}

// fuseMarkerPost sends an authenticated write to a fuse-marker endpoint.
func fuseMarkerPost(path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, registerBase()+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("only officers can manage Fuse markers")
	case http.StatusUnauthorized:
		return fmt.Errorf("link your Discord account first")
	default:
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
}

// SaveFuseMarker creates a shared marker (officer-only, enforced server-side).
func (a *App) SaveFuseMarker(zone, name string, x, y, z float64) error {
	if err := fuseMarkerPost("/fusemarkers", map[string]any{
		"zone": zone, "name": name, "x": x, "y": y, "z": z,
	}); err != nil {
		return err
	}
	invalidateFuseMarkers(zone)
	return nil
}

// DeleteFuseMarker removes a shared marker (officer-only, enforced server-side).
func (a *App) DeleteFuseMarker(id int, zone string) error {
	if err := fuseMarkerPost("/fusemarkers/delete", map[string]any{"id": id}); err != nil {
		return err
	}
	invalidateFuseMarkers(zone)
	return nil
}

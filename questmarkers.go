package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Temporary quest waypoints, placed by clicking a loc on a quest step
// (QuestEditor). Lifecycle is the point: a marker lives until the player has
// VISITED its zone and then left it — zoning out or camping — at which point
// it removes itself. Set one tonight, raid tomorrow, and it's gone once the
// errand is done, with nothing to clean up by hand.
//
// Persisted to the cache dir so markers survive an app restart (the visit may
// be days later); the visited flag is persisted too, so a restart while
// standing in the zone still clears the marker on the next zone-out.
type QuestMarker struct {
	Zone    string  `json:"zone"` // canonical zone display name
	Y       float64 `json:"y"`
	X       float64 `json:"x"`
	Label   string  `json:"label"`
	Visited bool    `json:"visited"`
	// Center: the spot is unknown, so the map draws this at the zone's
	// geometric center (only the frontend knows the map bounds). Y/X are 0
	// and meaningless when set.
	Center bool `json:"center,omitempty"`
}

var (
	qmMu     sync.Mutex
	qmList   []QuestMarker
	qmLoaded bool
)

func qmPath() string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "FuseBridgekeeper", "questmarkers.json")
}

func qmLoadLocked() {
	if qmLoaded {
		return
	}
	qmLoaded = true
	data, err := os.ReadFile(qmPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &qmList)
}

func qmSaveLocked() {
	data, err := json.MarshalIndent(qmList, "", " ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(qmPath()), 0700)
	_ = os.WriteFile(qmPath(), data, 0600)
}

// AddQuestMarker places (or re-places — same zone+label overwrites) a marker.
// Placed while already standing in the zone it counts as visited, so it still
// clears on the next zone-out or camp.
func (a *App) AddQuestMarker(zone string, y, x float64, label string) {
	zone = canonicalZone(strings.TrimSpace(zone))
	if zone == "" || strings.TrimSpace(label) == "" {
		return
	}
	m := QuestMarker{
		Zone: zone, Y: y, X: x, Label: strings.TrimSpace(label),
		Visited: strings.EqualFold(CurrentZone(), zone),
	}
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	for i := range qmList {
		if strings.EqualFold(qmList[i].Zone, m.Zone) && strings.EqualFold(qmList[i].Label, m.Label) {
			qmList[i] = m
			qmSaveLocked()
			return
		}
	}
	qmList = append(qmList, m)
	qmSaveLocked()
}

// addQuestMarkerAuto is the zone-nudge's marker drop: same upsert and
// lifecycle as AddQuestMarker (placed while standing in the zone it is
// already visited, so it retires on the next zone-out or camp — exactly the
// "temporary" a reminder wants), plus the center fallback for a spot nobody
// has recorded.
func addQuestMarkerAuto(zone string, y, x float64, label string, center bool) {
	zone = canonicalZone(strings.TrimSpace(zone))
	if zone == "" || strings.TrimSpace(label) == "" {
		return
	}
	m := QuestMarker{
		Zone: zone, Y: y, X: x, Label: strings.TrimSpace(label), Center: center,
		Visited: strings.EqualFold(CurrentZone(), zone),
	}
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	for i := range qmList {
		if strings.EqualFold(qmList[i].Zone, m.Zone) && strings.EqualFold(qmList[i].Label, m.Label) {
			qmList[i] = m
			qmSaveLocked()
			return
		}
	}
	qmList = append(qmList, m)
	qmSaveLocked()
}

// questMarkersRetireNeed removes markers pointing at a need — matched by the
// label's LAST segment ("<quest>: <need>"), not the whole label, so it also
// catches markers dropped by older clients whose prefix format differed (the
// full epic catalog name, before the "Ranger Epic" trim).
func questMarkersRetireNeed(need string) {
	need = strings.TrimSpace(need)
	if need == "" {
		return
	}
	needLC := strings.ToLower(need)
	suffix := ": " + needLC
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	changed := false
	kept := qmList[:0]
	for _, m := range qmList {
		l := strings.ToLower(strings.TrimSpace(m.Label))
		if l == needLC || strings.HasSuffix(l, suffix) {
			changed = true
			continue
		}
		kept = append(kept, m)
	}
	qmList = kept
	if changed {
		qmSaveLocked()
	}
}

// questMarkersRetireLabel removes a marker by its exact label, any zone — the
// retirement for flags whose label is not "<quest>: <need>" shaped (the
// medallion pieces), once the thing they point at is in hand.
func questMarkersRetireLabel(label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	changed := false
	kept := qmList[:0]
	for _, m := range qmList {
		if strings.EqualFold(m.Label, label) {
			changed = true
			continue
		}
		kept = append(kept, m)
	}
	qmList = kept
	if changed {
		qmSaveLocked()
	}
}

// RemoveQuestMarker deletes one marker by zone+label (the map's delete panel).
func (a *App) RemoveQuestMarker(zone, label string) {
	zone = canonicalZone(strings.TrimSpace(zone))
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	kept := qmList[:0]
	for _, m := range qmList {
		if strings.EqualFold(m.Zone, zone) && strings.EqualFold(m.Label, label) {
			continue
		}
		kept = append(kept, m)
	}
	qmList = kept
	qmSaveLocked()
}

// GetQuestMarkers returns the markers for one zone (display name, any known
// spelling), for the map to draw.
func (a *App) GetQuestMarkers(zone string) []QuestMarker {
	zone = canonicalZone(strings.TrimSpace(zone))
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	out := []QuestMarker{}
	for _, m := range qmList {
		if strings.EqualFold(m.Zone, zone) {
			out = append(out, m)
		}
	}
	return out
}

// questMarkersZoneChange runs on every zone the player is seen in: markers for
// THIS zone become visited; visited markers for any OTHER zone have served
// their purpose — the player was there and has now left — and are removed.
// Idempotent per zone, so the repeated sightings from /who footers are fine.
func questMarkersZoneChange(zone string) {
	if zone == "" {
		return
	}
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	changed := false
	kept := qmList[:0]
	for _, m := range qmList {
		if strings.EqualFold(m.Zone, zone) {
			if !m.Visited {
				m.Visited = true
				changed = true
			}
			kept = append(kept, m)
			continue
		}
		if m.Visited {
			changed = true // visited and now elsewhere — done with it
			continue
		}
		kept = append(kept, m)
	}
	qmList = kept
	if changed {
		qmSaveLocked()
	}
}

// questMarkersCamp treats camping/quitting like leaving the zone: any visited
// marker is done.
func questMarkersCamp() {
	qmMu.Lock()
	defer qmMu.Unlock()
	qmLoadLocked()
	changed := false
	kept := qmList[:0]
	for _, m := range qmList {
		if m.Visited {
			changed = true
			continue
		}
		kept = append(kept, m)
	}
	qmList = kept
	if changed {
		qmSaveLocked()
	}
}

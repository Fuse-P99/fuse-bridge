package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// World timers: the server-wide Game Time / Events / Boats board shown at the
// top of Timers > Current Timers. The server owns all the state
// (worldTimers.go there); this side only does two things:
//
//   - watches the log for the boats' zone-wide dock announcements and posts
//     a sighting (one sighting from anyone in the zone pins the whole loop
//     for the fleet), and
//   - proxies GET /worldtimers for the UI, with a short cache so the tab's
//     poll can't hammer the server.
//
// Both are gated by the "Boats & Zone Events" toggle (Settings.WorldTimers).

// boatAnnouncement maps a zone-wide log line prefix to the (boat, zone) pair
// the server expects. Strings verbatim from EqTool's Zones.cs; matched as
// prefixes the way EqTool's BoatParser does.
type boatAnnouncement struct {
	prefix string
	boat   string
	zone   string
}

var boatAnnouncements = []boatAnnouncement{
	{prefix: "Rack Stonebelly shouts, 'Da Barrel Barge will be here soon soon!'", boat: "barrel", zone: "oasis"},
	{prefix: "Rack Stonebelly shouts, 'Da Bloated Belly be leaving da Overdere now!'", boat: "bloated", zone: "overthere"},
	{prefix: "Glisse Bluesea shouts 'The Maiden's Voyage is now ready to be boarded.", boat: "maiden", zone: "butcher"},
	{prefix: "Glisse Bluesea shouts 'The Maiden's Voyage has departed the outpost at Firiona Vie.", boat: "maiden", zone: "firiona"},
	{prefix: "Frankel the Pirate says 'Thar she be mates. All aboard thats goin aboard!'", boat: "icebreaker", zone: "nro"},
}

// boatManualReport lets a player anchor a boat that makes no usable dock
// announcement by saying a phrase when it docks.
//
// The Bloated Belly is the case: it shouts only on DEPARTURE from Overthere,
// and nothing at all on the Timorous side, so a rider who watches it pull in
// has information the log never announces. Reported as event "dock", which the
// server back-dates by the leg's announcement→dock offset to recover the
// equivalent announcement time — saying "it arrived" and hearing "it left" are
// the same loop seen from two points, 50 seconds apart on this route.
type boatManualReport struct {
	phrase string // matched case-insensitively inside the player's own say
	boat   string
	zone   string
	// hint is surfaced in the Timers tab hover so the phrase is discoverable
	// where the boat is, not only in documentation.
	hint string
}

var boatManualReports = []boatManualReport{
	{
		phrase: "boat arrived at ot", boat: "bloated", zone: "overthere",
		hint: "This route has no arrival shout — type  /say Boat arrived at OT  as it docks in Overthere to update this timer for everyone.",
	},
}

// RecordBoatLine watches for a boat dock announcement (or a player's manual
// arrival report) and reports the sighting to the server. Call it for every raw
// log line.
func RecordBoatLine(line string) {
	msg := logMessageContent(line)
	if msg == "" {
		return
	}
	for _, a := range boatAnnouncements {
		if strings.HasPrefix(msg, a.prefix) {
			go postBoatSighting(a.boat, a.zone, "announce")
			return
		}
	}
	// Manual reports are accepted only from the player's OWN say. Anyone else
	// repeating the phrase — quoting it, joking about it — must not re-anchor a
	// timer the whole guild reads.
	if !strings.HasPrefix(msg, "You say") {
		return
	}
	lower := strings.ToLower(msg)
	for _, m := range boatManualReports {
		if strings.Contains(lower, m.phrase) {
			go postBoatSighting(m.boat, m.zone, "dock")
			return
		}
	}
}

func postBoatSighting(boat, zone, event string) {
	if !GetSettings().WorldTimers || !IsLinked() {
		return
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string]string{"boat": boat, "zone": zone, "event": event})
	req, err := http.NewRequest(http.MethodPost, base+"/boats", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// ── board fetch (UI) ────────────────────────────────────────────────────────

// WorldEvent mirrors the server's zone-event entry (Scout Charisa, Ring 8).
type WorldEvent struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Have      bool   `json:"have"`
	SpawnAtMs int64  `json:"spawn_at_ms"`
	RespawnMs int64  `json:"respawn_ms"`
	Marks     int    `json:"marks"` // "?" count: accuracy drift since a fresh ToD
	UpdatedMs int64  `json:"updated_ms"`
	// FromQuake: anchored on an earthquake (quake + 30m for Ring 8) rather
	// than on a Gynok report.
	FromQuake bool `json:"from_quake"`
}

// WorldBoat mirrors the server's boat entry. Dock times are the next docking
// instants; the loop repeats every PeriodMs, so the UI extrapolates past them.
type WorldBoat struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	EndA     string `json:"end_a"`
	EndB     string `json:"end_b"`
	Have     bool   `json:"have"`
	DockAMs  int64  `json:"dock_a_ms"`
	DockBMs  int64  `json:"dock_b_ms"`
	PeriodMs int64  `json:"period_ms"`
	SeenMs   int64  `json:"seen_ms"`
	// ManualHint tells the player how to report this boat by hand, for routes
	// with no usable announcement. Filled in locally from boatManualReports —
	// the phrase is defined next to the parser that matches it, so the two can't
	// drift apart. Empty for boats that anchor themselves.
	ManualHint string `json:"manual_hint"`
}

// WorldTimersData is the board handed to the Timers tab. Enabled reflects the
// "Boats & Zone Events" setting so the UI can hide the sections in one place.
type WorldTimersData struct {
	Enabled bool         `json:"enabled"`
	Events  []WorldEvent `json:"events"`
	Boats   []WorldBoat  `json:"boats"`
}

var (
	wtCacheMu sync.Mutex
	wtCache   WorldTimersData
	wtCacheAt time.Time
)

// fetchWorldTimers pulls the board from the server, caching briefly: the tab
// polls every few seconds and dock/spawn extrapolation happens client-side,
// so fresh-to-5s is plenty.
func fetchWorldTimers() WorldTimersData {
	out := WorldTimersData{Enabled: GetSettings().WorldTimers}
	if !out.Enabled || !IsLinked() {
		return out
	}

	wtCacheMu.Lock()
	if time.Since(wtCacheAt) < 5*time.Second {
		cached := wtCache
		wtCacheMu.Unlock()
		cached.Enabled = true
		return cached
	}
	wtCacheMu.Unlock()

	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/worldtimers", nil)
	if err != nil {
		return out
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return WorldTimersData{Enabled: true}
	}
	out.Enabled = true
	for i := range out.Boats {
		for _, m := range boatManualReports {
			if out.Boats[i].Key == m.boat {
				out.Boats[i].ManualHint = m.hint
				break
			}
		}
	}

	wtCacheMu.Lock()
	wtCache = out
	wtCacheAt = time.Now()
	wtCacheMu.Unlock()
	return out
}

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

// boatAnnouncement maps a dock-announcement log line prefix to the (boat, zone)
// pair the server expects. Strings verbatim from EqTool's Zones.cs; matched as
// prefixes the way EqTool's BoatParser does.
//
// `zone` names the END OF THE ROUTE the announcement anchors — not the zone the
// line is heard in. The two usually coincide, but not always: the Bloated
// Belly's "leaving da Overdere" call is made at the OASIS dock (the transfer
// chain runs Oasis → Timorous → Overthere), and it still anchors the Overthere
// leg. Setting it to where the line is heard would match no leg on that boat and
// silently stop it anchoring.
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

// boatManualReport lets a player anchor a boat by saying a phrase as it docks,
// reported as event "dock" so the server back-dates it by that leg's
// announcement→dock offset.
//
// Deliberately NOT advertised in the UI. These were added when the Bloated Belly
// looked like it had no usable announcement; it turned out to have one, called at
// the Oasis dock, so the route anchors itself whenever any relay client is in
// Oasis and players have no reason to be asked for anything.
//
// Retained because they still do something the shouts can't: a dock report pins
// the end it was made at exactly, which is how the Timorous-side offset gets
// measured rather than assumed.
type boatManualReport struct {
	phrase string // matched case-insensitively inside the player's own say
	boat   string
	zone   string
}

var boatManualReports = []boatManualReport{
	{phrase: "boat arrived at ot", boat: "bloated", zone: "overthere"},
	{phrase: "boat arrived at td", boat: "bloated", zone: "timorous"},
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
	// FromQuake: anchored on an earthquake (the Ring 8 roll lands 30m BEFORE
	// the quake) rather than on a Gynok report.
	FromQuake bool `json:"from_quake"`
	// WindowMs is a spawn-window half-width (the angry goblin): SpawnAtMs is
	// the window's center, the mob pops in [spawn−window, spawn+window].
	WindowMs int64 `json:"window_ms"`
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
}

// WorldTimersData is the board handed to the Timers tab. Enabled reflects the
// "Boats & Zone Events" setting so the UI can hide the sections in one place.
type WorldTimersData struct {
	Enabled bool         `json:"enabled"`
	Events  []WorldEvent `json:"events"`
	Boats   []WorldBoat  `json:"boats"`
	// LastQuakeMs is when the world last shook (0 = none on record).
	LastQuakeMs int64 `json:"last_quake_ms"`
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

	wtCacheMu.Lock()
	wtCache = out
	wtCacheAt = time.Now()
	wtCacheMu.Unlock()
	return out
}

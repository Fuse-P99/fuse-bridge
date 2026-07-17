package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The "/time" command prints the in-game hour on its "Game Time:" line, e.g.:
//   [Thu Jul 16 13:54:50 2026] Game Time: Thursday, July 12, 3247 - 10 PM
// We record that hour against the instant we saw it (this machine's clock) and
// use it to pin the game clock. The paired "Earth Time:" line is just the local
// time again, so it is ignored.
var gameTimeRE = regexp.MustCompile(`^Game Time:\s+\w+,\s+\w+\s+\d+,\s+\d+\s*-\s*(\d{1,2})\s*(AM|PM)`)

const gtMsPerHour = 180000 // 3 real minutes per in-game hour

const (
	gtKeepWindow = 12 * time.Hour
	gtMaxObs     = 500
)

// gameObs is one parsed /time sample: real instant (unix millis) + in-game hour.
type gameObs struct {
	earthMs int64
	hour    int
}

var (
	gtMu       sync.Mutex
	gtLocalObs []gameObs

	gtServerMu sync.Mutex
	gtServer   GameClockInfo
	gtServerAt time.Time
)

// GameClockInfo is the resolved game clock handed to the footer. The frontend
// extrapolates the minute-of-hour from the anchor each second.
type GameClockInfo struct {
	HaveGame       bool   `json:"have_game"`
	AnchorEarthMs  int64  `json:"anchor_earth_ms"`
	AnchorGameHour int    `json:"anchor_game_hour"`
	MsPerGameHour  int64  `json:"ms_per_game_hour"`
	Source         string `json:"source"` // "server" | "local" | ""
}

func gtTo24(h12 int, ampm string) int {
	h := h12 % 12
	if strings.EqualFold(ampm, "PM") {
		h += 12
	}
	return h
}

// RecordGameTimeLine watches for the /time output's "Game Time:" line, records
// the in-game hour against the current instant (kept locally for the offline
// fallback clock), and posts it to the server for fleet-wide aggregation. Call
// it for every raw log line.
func RecordGameTimeLine(line string) {
	m := gameTimeRE.FindStringSubmatch(logMessageContent(line))
	if m == nil {
		return
	}
	h12, _ := strconv.Atoi(m[1])
	hour := gtTo24(h12, m[2])
	nowMs := time.Now().UnixMilli()
	gtMu.Lock()
	gtLocalObs = append(gtLocalObs, gameObs{earthMs: nowMs, hour: hour})
	pruneLocalObsLocked(nowMs)
	gtMu.Unlock()
	go postGameTimeObs(hour)
}

func pruneLocalObsLocked(nowMs int64) {
	cut := nowMs - int64(gtKeepWindow/time.Millisecond)
	out := gtLocalObs[:0]
	for _, o := range gtLocalObs {
		if o.earthMs >= cut {
			out = append(out, o)
		}
	}
	gtLocalObs = out
	if len(gtLocalObs) > gtMaxObs {
		gtLocalObs = append([]gameObs(nil), gtLocalObs[len(gtLocalObs)-gtMaxObs:]...)
	}
}

// postGameTimeObs forwards a sample to the server. Gated by the "Game Time"
// filter and requires a linked token (the endpoint authenticates writes).
func postGameTimeObs(hour int) {
	if !GetSettings().GameTime || !IsLinked() {
		return
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string]any{"game_hour": hour})
	req, err := http.NewRequest(http.MethodPost, base+"/gametime", bytes.NewReader(body))
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

// GetGameClock resolves the footer clock, preferring the server's fleet anchor
// and falling back to this client's own observations when the server has none.
func GetGameClock() GameClockInfo {
	gtServerMu.Lock()
	srv := gtServer
	fresh := time.Since(gtServerAt) < 3*time.Minute
	gtServerMu.Unlock()
	if fresh && srv.HaveGame {
		srv.Source = "server"
		srv.MsPerGameHour = gtMsPerHour
		return srv
	}

	gtMu.Lock()
	obs := make([]gameObs, len(gtLocalObs))
	copy(obs, gtLocalObs)
	gtMu.Unlock()
	if am, hr, _, ok := computeGameAnchor(obs); ok {
		return GameClockInfo{
			HaveGame:       true,
			AnchorEarthMs:  am,
			AnchorGameHour: hr,
			MsPerGameHour:  gtMsPerHour,
			Source:         "local",
		}
	}
	if srv.HaveGame { // a stale server value still beats nothing
		srv.Source = "server"
		srv.MsPerGameHour = gtMsPerHour
		return srv
	}
	return GameClockInfo{MsPerGameHour: gtMsPerHour}
}

// startGameClockSync periodically pulls the server's aggregated anchor so the
// footer reflects fleet-wide accuracy without a fetch on every UI tick.
func startGameClockSync() {
	go func() {
		fetch := func() {
			if info, ok := fetchGameClock(); ok {
				gtServerMu.Lock()
				gtServer = info
				gtServerAt = time.Now()
				gtServerMu.Unlock()
			}
		}
		fetch()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			fetch()
		}
	}()
}

func fetchGameClock() (GameClockInfo, bool) {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/gametime", nil)
	if err != nil {
		return GameClockInfo{}, false
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return GameClockInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GameClockInfo{}, false
	}
	var info GameClockInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return GameClockInfo{}, false
	}
	return info, true
}

// computeGameAnchor mirrors the server's aggregation for the local fallback:
// prefer the tightest transition bracket (two samples one hour apart straddling
// the tick), else the newest sample assumed mid-hour. A bracket is accepted only
// if it still predicts the newest sample's hour.
func computeGameAnchor(obs []gameObs) (anchorMs int64, anchorHour int, halfWidthMs int64, ok bool) {
	if len(obs) == 0 {
		return 0, 0, 0, false
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].earthMs < obs[j].earthMs })
	const R = int64(gtMsPerHour)

	newest := obs[len(obs)-1]
	anchorMs = newest.earthMs - R/2
	anchorHour = newest.hour
	halfWidthMs = R / 2
	ok = true

	for i := 1; i < len(obs); i++ {
		a, b := obs[i-1], obs[i]
		if ((b.hour-a.hour)%24+24)%24 != 1 {
			continue
		}
		gap := b.earthMs - a.earthMs
		if gap < 0 || gap >= R {
			continue
		}
		half := gap / 2
		if half >= halfWidthMs {
			continue
		}
		candMs := a.earthMs + gap/2
		if predictGameHour(candMs, b.hour, newest.earthMs) != newest.hour {
			continue
		}
		halfWidthMs = half
		anchorMs = candMs
		anchorHour = b.hour
	}
	return
}

func predictGameHour(anchorMs int64, anchorHour int, atMs int64) int {
	gh := float64(anchorHour) + float64(atMs-anchorMs)/float64(gtMsPerHour)
	h := int(math.Floor(gh)) % 24
	if h < 0 {
		h += 24
	}
	return h
}

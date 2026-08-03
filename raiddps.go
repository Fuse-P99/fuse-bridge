package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Raid DPS rollup: unlike the Threat Meter (first-person only), this parses
// EVERY player's melee damage on the mob we're fighting, because third-person
// damage lines name their attacker. Each linked client posts what IT saw; the
// server merges by taking the MAXIMUM per attacker.
//
// Why max and never sum: every client in range logs the same lines, so summing
// multiplies the raid's damage by the number of reporters. Range only ever
// causes a client to MISS lines, never to invent them, so every report is a
// lower bound on the truth and the largest report is the tightest bound.
//
// The flip side of max is that it will happily elect an over-reporter and can
// never be corrected downward, so nothing here may report damage it isn't sure
// of. Two rules follow, and both are load-bearing:
//
//   - Only wall-clock-fresh lines are posted. A client that starts up and tails
//     an existing log replays old fights; under max, one stale-but-larger
//     number would poison the mob's whole board (same reasoning as
//     MaybeSendThreat's freshness gate).
//   - Multi-word attacker names are dropped. They're pets and NPC guards, and
//     they merge under one name — "a Drakkel Dire Wolf" once totalled 59,535
//     across a dozen different pets in a single AoW parse.
//
// Non-melee damage (damage shields, weapon procs, nukes) is tracked SEPARATELY
// and only ever for ourselves. The log line for it — "<mob> was hit by
// non-melee for N points of damage." — names no source, and a character only
// ever sees their own: a tank's 456 damage-shield ticks appear in the tank's
// log and nobody else's. It can never be cross-checked by max, so the server
// takes it from the owner's own client alone.

var (
	// Third-person and first-person melee damage. The attacker is captured;
	// "You" is normalized to our own name so both views of the same swing land
	// on one row.
	rdpsHitRE = regexp.MustCompile(`^(.+?) (?:` + threatVerbs + `|hits|kicks|slashes|crushes|pierces|bashes|slams|strikes|punches|backstabs|bites|claws|smashes|slices|gores|mauls|rends|burns|stings|sweeps) (.+?) for (\d+) points? of damage\.$`)
	// Anonymous non-melee on a mob: our own damage shield or proc. No source.
	rdpsNonMeleeRE = regexp.MustCompile(`^(.+?) was hit by non-melee for (\d+) points? of damage\.$`)
	// Name-form non-melee ("Carboload hit X for N points of non-melee damage.")
	// — the old client's rendering of OUR own proc/nuke damage.
	rdpsSelfNonMeleeRE = regexp.MustCompile("^([\\w`' -]+?) hit (.+?) for (\\d+) points? of non-melee damage\\.$")
)

// rdpsFight is one mob's damage board as this client saw it.
type rdpsFight struct {
	display  string
	first    time.Time
	last     time.Time
	melee    map[string]int // attacker (as logged) → damage seen
	selfNonM int            // OUR non-melee only: damage shield + procs
}

var (
	rdpsMu      sync.Mutex
	rdpsFights  = map[string]*rdpsFight{} // normThreatMob → board
	rdpsCurrent string
	rdpsSendAt  time.Time
)

const (
	rdpsIdleReset = 5 * time.Minute
	rdpsPostEvery = 10 * time.Second
	// A line must be this fresh in WALL CLOCK terms to count. Startup replay
	// stamps entries with old log times; posting those would hand the server a
	// number max can never walk back.
	rdpsFreshWindow = 30 * time.Second
)

// rdpsAttacker normalizes an attacker name, returning "" for anything we must
// not attribute: pets and NPCs (multi-word names), and the mob itself.
func rdpsAttacker(name, mobDisplay string) string {
	n := strings.TrimSpace(name)
	if n == "" || strings.EqualFold(n, mobDisplay) {
		return ""
	}
	if n == "You" || n == "YOU" {
		if currentCharName == "" {
			return ""
		}
		return currentCharName
	}
	// Players are one word. "a Drakkel Dire Wolf", "Captain Bvellos", "The
	// Avatar of War" are not players and must never own a damage row.
	if strings.ContainsAny(n, " `'") {
		return ""
	}
	return n
}

// RecordRaidDPSLine feeds one raw log line to the raid damage rollup. Called
// for every line, so the cheap suffix gate comes first — in a 73-player AoW
// raid this sees ~150 lines/second.
func RecordRaidDPSLine(line string) {
	content := logMessageContent(line)
	// Two distinct tails: melee and the anonymous shield line end "... points
	// of damage.", our own proc/nuke ends "... of non-melee damage." — the
	// second does NOT match the first, so both must be named here.
	if !strings.HasSuffix(content, " of damage.") &&
		!strings.HasSuffix(content, " of non-melee damage.") {
		return
	}
	at := logLineTime(line)
	if at.IsZero() {
		at = time.Now()
	}
	// Replay guard: only lines whose log time is close to now are real.
	if d := time.Since(at); d > rdpsFreshWindow || d < -rdpsFreshWindow {
		return
	}

	rdpsMu.Lock()
	defer rdpsMu.Unlock()
	rdpsPruneLocked(at)

	// Non-melee first: its suffix ("of non-melee damage.") can't reach the
	// melee regex, but the anonymous shield form ends in " of damage." and
	// would otherwise be parsed as a melee swing by "<mob> was hit by ...".
	if strings.HasSuffix(content, "of non-melee damage.") {
		if m := rdpsSelfNonMeleeRE.FindStringSubmatch(content); m != nil &&
			(m[1] == "You" || strings.EqualFold(m[1], currentCharName)) {
			dmg, _ := strconv.Atoi(m[3])
			f := rdpsEnsureLocked(m[2], at)
			f.selfNonM += dmg
			f.last = at
		}
		return
	}
	if m := rdpsNonMeleeRE.FindStringSubmatch(content); m != nil {
		// Anonymous — only ever OUR damage shield or proc, since a character
		// sees no one else's. Credited to us, never to a named row.
		dmg, _ := strconv.Atoi(m[2])
		f := rdpsEnsureLocked(m[1], at)
		f.selfNonM += dmg
		f.last = at
		return
	}
	if m := rdpsHitRE.FindStringSubmatch(content); m != nil {
		f := rdpsEnsureLocked(m[2], at)
		who := rdpsAttacker(m[1], f.display)
		if who == "" {
			return
		}
		dmg, _ := strconv.Atoi(m[3])
		f.melee[who] += dmg
		f.last = at
	}
}

func rdpsEnsureLocked(mob string, at time.Time) *rdpsFight {
	norm := normThreatMob(mob)
	f := rdpsFights[norm]
	if f == nil {
		f = &rdpsFight{display: strings.TrimSpace(mob), first: at, melee: map[string]int{}}
		rdpsFights[norm] = f
	}
	rdpsCurrent = norm
	return f
}

func rdpsPruneLocked(now time.Time) {
	for k, f := range rdpsFights {
		if now.Sub(f.last) > rdpsIdleReset {
			delete(rdpsFights, k)
			if rdpsCurrent == k {
				rdpsCurrent = ""
			}
		}
	}
}

// RaidDPSResetMob drops a dead mob's board — the fight is over, and the next
// spawn of the same name starts clean.
func RaidDPSResetMob(mob string) {
	norm := normThreatMob(mob)
	rdpsMu.Lock()
	delete(rdpsFights, norm)
	if rdpsCurrent == norm {
		rdpsCurrent = ""
	}
	rdpsMu.Unlock()
}

// rdpsRowWire is one attacker's damage as this client saw it.
type rdpsRowWire struct {
	Name  string `json:"name"`
	Melee int    `json:"melee"`
}

type rdpsPostWire struct {
	Toon string `json:"toon"`
	Mob  string `json:"mob"`
	// EngagedS is how long this client has been watching THIS mob — the
	// server needs a clock to turn damage into a rate.
	EngagedS int           `json:"engaged_s"`
	Rows     []rdpsRowWire `json:"rows"`
	// SelfNonMelee is our own damage shield + proc damage. Nobody else can
	// see it, so the server takes it from us alone rather than by max.
	SelfNonMelee int `json:"self_non_melee"`
}

// ── officer read path ────────────────────────────────────────────────────────

// RaidDPSPlayerUI / RaidDPSClassUI / RaidDPSUI mirror the server's response.
type RaidDPSPlayerUI struct {
	Name  string  `json:"name"`
	Class string  `json:"class"`
	Melee int     `json:"melee"`
	Other int     `json:"non_melee"`
	Total int     `json:"total"`
	DPS   float64 `json:"dps"`
	Pct   float64 `json:"pct"`
	Dead  bool    `json:"dead"`
	// Reporters/Spread expose how well-witnessed a number is: how many clients
	// saw this attacker, and how far apart their totals were.
	Reporters int `json:"reporters"`
	Spread    int `json:"spread"`
}

// RaidDPSThreatUI is one row of the estimated-threat column.
type RaidDPSThreatUI struct {
	Name   string `json:"name"`
	Class  string `json:"class"`
	Rank   int    `json:"rank"` // position in the FULL order; 0 = not relaying
	Threat int    `json:"threat"`
	Dead   bool   `json:"dead"`
	IsTank bool   `json:"is_tank"`
}

type RaidDPSClassUI struct {
	Class string  `json:"class"`
	Total int     `json:"total"`
	DPS   float64 `json:"dps"`
	Pct   float64 `json:"pct"`
	Count int     `json:"count"`
}

type RaidDPSUI struct {
	Officer  bool              `json:"officer"`
	Mob      string            `json:"mob"`
	EngagedS int               `json:"engaged_s"`
	Total    int               `json:"total"`
	RaidDPS  float64           `json:"raid_dps"`
	Top      []RaidDPSPlayerUI `json:"top"`
	Classes  []RaidDPSClassUI  `json:"classes"`
	Threat   []RaidDPSThreatUI `json:"threat"`
}

var (
	rdpsTblMu sync.Mutex
	rdpsTbl   RaidDPSUI
	rdpsTblAt time.Time
)

const rdpsTblTTL = 3 * time.Second

// GetRaidDPS returns the raid damage board for the mob the raid is on.
// Officer-only, like the threat gauge; cached so the card's poll costs one
// round-trip every few seconds.
func (a *App) GetRaidDPS() RaidDPSUI {
	if !IsLinked() || !isOfficerCached() {
		return RaidDPSUI{Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}, Threat: []RaidDPSThreatUI{}}
	}
	rdpsTblMu.Lock()
	if time.Since(rdpsTblAt) < rdpsTblTTL {
		out := rdpsTbl
		rdpsTblMu.Unlock()
		return out
	}
	rdpsTblMu.Unlock()

	out := RaidDPSUI{Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}, Threat: []RaidDPSThreatUI{}}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/raiddps", nil)
	if err != nil {
		return out
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(resp.Body).Decode(&out)
	}
	out.Officer = true
	if out.Top == nil {
		out.Top = []RaidDPSPlayerUI{}
	}
	if out.Classes == nil {
		out.Classes = []RaidDPSClassUI{}
	}
	if out.Threat == nil {
		out.Threat = []RaidDPSThreatUI{}
	}
	rdpsTblMu.Lock()
	rdpsTbl, rdpsTblAt = out, time.Now()
	rdpsTblMu.Unlock()
	return out
}

// MaybeSendRaidDPS posts this client's view of the current fight's damage,
// throttled. Fire-and-forget, like the threat snapshot.
func (s *Sender) MaybeSendRaidDPS() {
	if !IsLinked() || currentCharName == "" {
		return
	}
	rdpsMu.Lock()
	if time.Since(rdpsSendAt) < rdpsPostEvery {
		rdpsMu.Unlock()
		return
	}
	f := rdpsFights[rdpsCurrent]
	if f == nil || time.Since(f.last) > rdpsFreshWindow {
		rdpsMu.Unlock()
		return
	}
	rdpsSendAt = time.Now()
	payload := rdpsPostWire{
		Toon:         currentCharName,
		Mob:          f.display,
		EngagedS:     int(f.last.Sub(f.first).Seconds()),
		SelfNonMelee: f.selfNonM,
		Rows:         make([]rdpsRowWire, 0, len(f.melee)),
	}
	for name, dmg := range f.melee {
		payload.Rows = append(payload.Rows, rdpsRowWire{Name: name, Melee: dmg})
	}
	rdpsMu.Unlock()
	if len(payload.Rows) == 0 && payload.SelfNonMelee == 0 {
		return
	}

	go func() {
		base := strings.TrimSuffix(s.serverURL, "/submit")
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, base+"/raiddps", bytes.NewReader(body))
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

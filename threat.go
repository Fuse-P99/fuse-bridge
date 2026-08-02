package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Threat Meter: parses the player's own combat lines into a per-mob damage
// and estimated-hate ledger. The damage parse opens and closes purely on
// damage matching — any melee fight, raid or not. Snapshots are relayed to
// the server (all linked clients; the tank may not be an officer), and the
// officer-only overlay compares the viewer's hate to the main tank's. Raid
// identification only gates the threat-gauge section, never the parse.

// ── tuning ───────────────────────────────────────────────────────────────────

// ThreatTuning mirrors the server's table (threatMeter.go); the raid-wide
// numbers live server-side so every client computes with identical values.
type ThreatTuning struct {
	ProcHate         int              `json:"proc_hate"`
	MissFactor       float64          `json:"miss_factor"`
	ProcWindowS      int              `json:"proc_window_s"`
	SwingHate        map[string]int   `json:"swing_hate"`
	SpecialHate      map[string]int   `json:"special_hate"`
	Reducers         map[string]int   `json:"reducers"`
	ReducerWindowS   int              `json:"reducer_window_s"`
	Gauge            ThreatGaugeZones `json:"gauge"`
	DPSIdleResetS    int              `json:"dps_idle_reset_s"`
	ThreatIdleResetS int              `json:"threat_idle_reset_s"`
	PostIntervalS    int              `json:"post_interval_s"`
	ServerEntryTTLS  int              `json:"server_entry_ttl_s"`
}

// ThreatGaugeZones are fractions of the reference (tank) threat.
type ThreatGaugeZones struct {
	GreenMax  float64 `json:"green_max"`
	YellowMax float64 `json:"yellow_max"`
	Cap       float64 `json:"cap"`
}

// defaultThreatTuning must stay value-identical to the server's compiled
// defaults so an unlinked or unreachable client computes the same numbers.
func defaultThreatTuning() ThreatTuning {
	return ThreatTuning{
		ProcHate:    400,
		MissFactor:  1.0,
		ProcWindowS: 8,
		SwingHate: map[string]int{
			"warrior": 120, "monk": 90, "rogue": 80, "ranger": 80,
			"paladin": 100, "shadow knight": 100, "bard": 60, "default": 50,
		},
		SpecialHate: map[string]int{
			"backstab": 400, "kick": 150, "flying_kick": 300, "bash": 200, "slam": 150,
		},
		Reducers: map[string]int{
			"concussion": 400, "jolt": 500, "cinder jolt": 500, "evade": 500,
		},
		ReducerWindowS:   6,
		Gauge:            ThreatGaugeZones{GreenMax: 0.70, YellowMax: 0.90, Cap: 1.50},
		DPSIdleResetS:    20,
		ThreatIdleResetS: 300,
		PostIntervalS:    2,
		ServerEntryTTLS:  90,
	}
}

var (
	threatTunMu  sync.Mutex
	threatTun    = defaultThreatTuning()
	threatTunAt  time.Time // last successful fetch
	threatTunTry time.Time // last attempt (throttles retries on failure)
)

const threatTunTTL = 30 * time.Minute

// threatTuningCached returns the current tuning table, kicking off a
// background /threat/config refresh when stale (mirrors ensureZoneIndex's
// lazy TTL). The first fight after startup may briefly use the compiled
// defaults; they match the server's own defaults.
func threatTuningCached() ThreatTuning {
	threatTunMu.Lock()
	stale := time.Since(threatTunAt) >= threatTunTTL && time.Since(threatTunTry) >= time.Minute
	if stale {
		threatTunTry = time.Now()
	}
	tun := threatTun
	threatTunMu.Unlock()
	if stale && IsLinked() {
		go fetchThreatTuning()
	}
	return tun
}

func storeThreatTuning(tun ThreatTuning) {
	threatTunMu.Lock()
	threatTun = tun
	threatTunAt = time.Now()
	threatTunMu.Unlock()
}

func fetchThreatTuning() {
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/threat/config", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var r struct {
		Config ThreatTuning `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return
	}
	storeThreatTuning(r.Config)
}

// ── parser ───────────────────────────────────────────────────────────────────

// First-person combat grammar (verbs from the eqlogparser project, classic
// client forms). Go RE2 has no lookahead, so the non-melee line is matched
// BEFORE the melee line; the melee RE's anchored "points of damage." tail
// cannot match a "points of non-melee damage." line anyway.
const threatVerbs = `hit|kick|slash|crush|pierce|bash|slam|strike|punch|backstab|bite|claw|smash|slice|gore|maul|rend|burn|sting|sweep`

var (
	threatNonMeleeRE = regexp.MustCompile(`^You hit (.+?) for (\d+) points? of non-melee damage\.$`)
	threatMeleeHitRE = regexp.MustCompile(`^You (` + threatVerbs + `) (.+?) for (\d+) points? of damage\.$`)
	threatMissRE     = regexp.MustCompile(`^You try to (` + threatVerbs + `)(?: on)? (.+?), but .+!$`)
	threatYouSlainRE = regexp.MustCompile(`^You have slain (.+?)!$`)
	threatSlainRE    = regexp.MustCompile(`^(.+?) has been slain by .+!$`)
	threatCastRE     = regexp.MustCompile("^You begin casting ([\\w`' -]+)\\.$")
	// Landing lines name the target, not the caster — attribution to our own
	// cast rides the begin-casting window below. The Jolt line appears both
	// as "X's head snaps back." and the raw-data "X 's head snaps back."
	threatConcOkRE = regexp.MustCompile(`^(.+?) staggers from a blow to the head\.$`)
	threatJoltOkRE = regexp.MustCompile("^(.+?) ?`?'?s head snaps back\\.$")
	threatResistRE = regexp.MustCompile("^Your target resisted the ([\\w`' -]+) spell\\.$")
)

const (
	threatEvadeOk   = "You have momentarily ducked away from the main combat."
	threatEvadeFail = "Your attempts at ducking clear of combat fail."
)

// mobThreat is one mob's ledger entry. threat is a float so miss_factor can
// scale swings; it's rounded at the wire.
type mobThreat struct {
	display   string    // first-seen capitalization for the overlay header
	firstHit  time.Time // engagement start (log time)
	lastAct   time.Time
	lastMelee time.Time // gates the proc heuristic
	damage    int
	threat    float64
}

// ThreatToolsUI counts hate-reducer outcomes for the current engagement.
type ThreatToolsUI struct {
	ConcOK    int `json:"conc_ok"`
	ConcFail  int `json:"conc_fail"`
	JoltOK    int `json:"jolt_ok"`
	JoltFail  int `json:"jolt_fail"`
	EvadeOK   int `json:"evade_ok"`
	EvadeFail int `json:"evade_fail"`
}

var (
	threatMu        sync.Mutex
	threatMobs      = map[string]*mobThreat{} // normThreatMob(name) → entry
	threatCurrent   string                    // most recent target; drives overlay + snapshots
	threatPendSpell string                    // lower spell name of our in-flight reducer cast
	threatPendAt    time.Time
	threatTools     ThreatToolsUI
)

// normThreatMob must mirror the server's twin so slain resets and table keys
// agree: case, tick style and spacing never split one mob into two keys.
func normThreatMob(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "'", "`")
	return strings.Join(strings.Fields(s), " ")
}

// threatEnsureLocked returns the ledger entry for a mob, creating it (or
// restarting its DPS fight window after an idle gap) as needed. Reports
// whether the engagement view changed in a way worth a UI nudge.
func threatEnsureLocked(norm, display string, at time.Time, tun ThreatTuning) (*mobThreat, bool) {
	changed := false
	mt := threatMobs[norm]
	if mt == nil {
		mt = &mobThreat{display: display, firstHit: at}
		threatMobs[norm] = mt
		changed = true
	} else if dpsIdle := threatDPSIdle(tun); at.Sub(mt.lastAct) > dpsIdle {
		// The previous fight against this mob ended by idle; a fresh swing
		// starts a new parse window. Accumulated hate persists — the mob
		// still remembers us until death or the long threat idle.
		mt.firstHit = at
		mt.damage = 0
		changed = true
	}
	if threatCurrent != norm {
		threatCurrent = norm
		changed = true
	}
	return mt, changed
}

func threatDPSIdle(tun ThreatTuning) time.Duration {
	if tun.DPSIdleResetS <= 0 {
		return 20 * time.Second
	}
	return time.Duration(tun.DPSIdleResetS) * time.Second
}

func threatIdle(tun ThreatTuning) time.Duration {
	if tun.ThreatIdleResetS <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(tun.ThreatIdleResetS) * time.Second
}

// threatSwingHate maps one landed/attempted swing to hate: activated attacks
// (backstab, kick, bash, slam) use the special table — monks' Flying Kick
// logs as plain "kick", so their kick maps to flying_kick — and everything
// else is a plain swing at the per-class rate.
func threatSwingHate(verb string, tun ThreatTuning) float64 {
	class := strings.ToLower(strings.TrimSpace(classForCurrentChar()))
	switch verb {
	case "backstab":
		return float64(tun.SpecialHate["backstab"])
	case "kick":
		if class == "monk" {
			return float64(tun.SpecialHate["flying_kick"])
		}
		return float64(tun.SpecialHate["kick"])
	case "bash":
		return float64(tun.SpecialHate["bash"])
	case "slam":
		return float64(tun.SpecialHate["slam"])
	}
	if v, ok := tun.SwingHate[class]; ok && class != "" {
		return float64(v)
	}
	return float64(tun.SwingHate["default"])
}

func pruneThreatMobsLocked(now time.Time, tun ThreatTuning) {
	cutoff := now.Add(-threatIdle(tun))
	for k, mt := range threatMobs {
		if mt.lastAct.Before(cutoff) && !mt.firstHit.After(cutoff) {
			delete(threatMobs, k)
			if threatCurrent == k {
				threatCurrent = ""
			}
		}
	}
}

// RecordThreatLine feeds one raw tailed log line to the threat ledger.
// Called for every line from main; cheap gates come first.
func RecordThreatLine(line string) {
	content := logMessageContent(line)
	// Shortest line of interest: "You have slain X!" with a tiny name.
	if len(content) < 12 {
		return
	}
	// Everything of interest either starts with "You"/"Your" or is a slain
	// or reducer-landing line with a fixed shape.
	if !strings.HasPrefix(content, "You") &&
		!strings.Contains(content, " has been slain by ") &&
		!strings.HasSuffix(content, "staggers from a blow to the head.") &&
		!strings.HasSuffix(content, "s head snaps back.") {
		return
	}
	// The log's own timestamp, not the wall clock: the tailer can replay a
	// burst of historical combat at startup (randoms.go has the long story).
	at := logLineTime(line)
	if at.IsZero() {
		at = time.Now()
	}
	tun := threatTuningCached()

	changed := false
	threatMu.Lock()
	pruneThreatMobsLocked(at, tun)

	switch {
	// Non-melee before melee: RE2 has no lookahead (see grammar note above).
	case strings.HasSuffix(content, "of non-melee damage."):
		if m := threatNonMeleeRE.FindStringSubmatch(content); m != nil {
			dmg, _ := strconv.Atoi(m[2])
			norm := normThreatMob(m[1])
			mt, ch := threatEnsureLocked(norm, m[1], at, tun)
			changed = ch
			mt.damage += dmg
			// A weapon proc can only come off a swing; a non-melee hit with
			// no recent melee is a (out-of-scope) nuke — damage only.
			if !mt.lastMelee.IsZero() && at.Sub(mt.lastMelee) <= time.Duration(tun.ProcWindowS)*time.Second {
				mt.threat += float64(tun.ProcHate)
			}
			mt.lastAct = at
		}

	// " of damage." covers both "points" and the singular "1 point"; the
	// non-melee line ends "non-melee damage." and can't reach this case.
	case strings.HasSuffix(content, " of damage."):
		if m := threatMeleeHitRE.FindStringSubmatch(content); m != nil {
			dmg, _ := strconv.Atoi(m[3])
			norm := normThreatMob(m[2])
			mt, ch := threatEnsureLocked(norm, m[2], at, tun)
			changed = ch
			mt.damage += dmg
			mt.threat += threatSwingHate(m[1], tun)
			mt.lastMelee = at
			mt.lastAct = at
		}

	case strings.HasPrefix(content, "You try to "):
		if m := threatMissRE.FindStringSubmatch(content); m != nil {
			// A missed swing still aggros (miss_factor, owner-tuned) and
			// still opens the engagement — the parse follows swings, not
			// raid identification.
			norm := normThreatMob(m[2])
			mt, ch := threatEnsureLocked(norm, m[2], at, tun)
			changed = ch
			mt.threat += threatSwingHate(m[1], tun) * tun.MissFactor
			mt.lastMelee = at
			mt.lastAct = at
		}

	case content == threatEvadeOk:
		threatTools.EvadeOK++
		if mt := threatMobs[threatCurrent]; mt != nil {
			mt.threat -= float64(tun.Reducers["evade"])
			if mt.threat < 0 {
				mt.threat = 0
			}
		}

	case content == threatEvadeFail:
		threatTools.EvadeFail++

	case strings.HasPrefix(content, "You begin casting "):
		if m := threatCastRE.FindStringSubmatch(content); m != nil {
			spell := strings.ToLower(strings.TrimSpace(m[1]))
			if _, ok := tun.Reducers[spell]; ok && spell != "evade" {
				threatPendSpell, threatPendAt = spell, at
			}
		}

	case strings.HasPrefix(content, "Your target resisted the "):
		if m := threatResistRE.FindStringSubmatch(content); m != nil {
			spell := strings.ToLower(strings.TrimSpace(m[1]))
			if spell == threatPendSpell && at.Sub(threatPendAt) <= time.Duration(tun.ReducerWindowS)*time.Second {
				if spell == "concussion" {
					threatTools.ConcFail++
				} else {
					threatTools.JoltFail++
				}
				threatPendSpell = ""
			}
		}

	case strings.HasSuffix(content, "staggers from a blow to the head."):
		threatReducerLandedLocked(content, threatConcOkRE, "concussion", at, tun)

	case strings.HasSuffix(content, "s head snaps back."):
		threatReducerLandedLocked(content, threatJoltOkRE, "jolt", at, tun)

	case strings.HasPrefix(content, "You have slain "):
		if m := threatYouSlainRE.FindStringSubmatch(content); m != nil {
			changed = threatMobDiedLocked(normThreatMob(m[1]))
		}

	default:
		if m := threatSlainRE.FindStringSubmatch(content); m != nil {
			changed = threatMobDiedLocked(normThreatMob(m[1]))
		}
	}
	threatMu.Unlock()

	if changed {
		emitThreatChanged()
	}
}

// threatReducerLandedLocked attributes a Concussion/Jolt landing line to our
// own in-flight cast (the line names the target, never the caster — an
// unclaimed landing is a guildmate's and is ignored).
func threatReducerLandedLocked(content string, re *regexp.Regexp, kind string, at time.Time, tun ThreatTuning) {
	if threatPendSpell == "" || at.Sub(threatPendAt) > time.Duration(tun.ReducerWindowS)*time.Second {
		return
	}
	isJolt := threatPendSpell == "jolt" || threatPendSpell == "cinder jolt"
	if (kind == "concussion") == isJolt {
		return // landing line doesn't match the spell we cast
	}
	m := re.FindStringSubmatch(content)
	if m == nil {
		return
	}
	if kind == "concussion" {
		threatTools.ConcOK++
	} else {
		threatTools.JoltOK++
	}
	// Reduce hate on the mob the spell named when we track it, else on the
	// current target.
	target := normThreatMob(m[1])
	mt := threatMobs[target]
	if mt == nil {
		mt = threatMobs[threatCurrent]
	}
	if mt != nil {
		mt.threat -= float64(tun.Reducers[threatPendSpell])
		if mt.threat < 0 {
			mt.threat = 0
		}
	}
	threatPendSpell = ""
}

// threatMobDiedLocked drops a dead mob's ledger; per-engagement tool counters
// reset with the current target's death.
func threatMobDiedLocked(norm string) bool {
	if _, ok := threatMobs[norm]; !ok {
		return false
	}
	delete(threatMobs, norm)
	if threatCurrent == norm {
		threatCurrent = ""
		threatTools = ThreatToolsUI{}
	}
	return true
}

func emitThreatChanged() {
	if v3App != nil {
		v3App.Event.Emit("threat-changed")
	}
}

// ── uplink ───────────────────────────────────────────────────────────────────

// threatSnapWire mirrors the server's threatSnapJSON.
type threatSnapWire struct {
	Toon     string  `json:"toon"`
	Class    string  `json:"class"`
	Mob      string  `json:"mob"`
	Threat   int     `json:"threat"`
	DPS      float64 `json:"dps"`
	EngagedS int     `json:"engaged_s"`
}

var (
	threatSendMu   sync.Mutex
	threatSendLast time.Time
)

// MaybeSendThreat posts the current engagement's snapshot, throttled to the
// tuned interval. Fire-and-forget like SendMapLoc. The freshness gate uses
// the wall clock on purpose: startup replay stamps entries with old log
// times, and those must never be posted as live threat.
func (s *Sender) MaybeSendThreat() {
	if !IsLinked() || currentCharName == "" {
		return
	}
	tun := threatTuningCached()
	interval := time.Duration(tun.PostIntervalS) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	threatSendMu.Lock()
	if time.Since(threatSendLast) < interval {
		threatSendMu.Unlock()
		return
	}
	threatSendLast = time.Now()
	threatSendMu.Unlock()

	threatMu.Lock()
	mt := threatMobs[threatCurrent]
	if mt == nil || time.Since(mt.lastAct) > 10*time.Second {
		threatMu.Unlock()
		return
	}
	payload := threatSnapWire{
		Toon:     currentCharName,
		Class:    classForCurrentChar(),
		Mob:      mt.display,
		Threat:   int(mt.threat + 0.5),
		DPS:      threatDPSLocked(mt, time.Now(), tun),
		EngagedS: threatElapsedLocked(mt, time.Now(), tun),
	}
	threatMu.Unlock()

	go func() {
		base := strings.TrimSuffix(s.serverURL, "/submit")
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, base+"/threat", bytes.NewReader(body))
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

// threatElapsedLocked is the live fight clock: it runs to "now" while the
// fight is warm and freezes at the last action once idle.
func threatElapsedLocked(mt *mobThreat, now time.Time, tun ThreatTuning) int {
	end := now
	if now.Sub(mt.lastAct) > threatDPSIdle(tun) {
		end = mt.lastAct
	}
	sec := int(end.Sub(mt.firstHit).Seconds())
	if sec < 1 {
		sec = 1
	}
	return sec
}

func threatDPSLocked(mt *mobThreat, now time.Time, tun ThreatTuning) float64 {
	return float64(mt.damage) / float64(threatElapsedLocked(mt, now, tun))
}

// ── officer table fetch + bound getter ───────────────────────────────────────

// threatTableWire mirrors the server's threatTableJSON.
type threatTableWire struct {
	Mob           string           `json:"mob"`
	RaidActive    bool             `json:"raid_active"`
	Tank          *threatSnapWire  `json:"tank"`
	TankSource    string           `json:"tank_source"`
	Own           *threatSnapWire  `json:"own"`
	Others        []threatSnapWire `json:"others"`
	Config        ThreatTuning     `json:"config"`
	ConfigVersion int64            `json:"config_version"`
}

var (
	threatTblMu  sync.Mutex
	threatTbl    *threatTableWire
	threatTblMob string
	threatTblAt  time.Time
)

const threatTblTTL = 2 * time.Second

// fetchThreatTable returns the server threat table for a mob, cached briefly
// so the overlay's 1s poll costs one HTTP round-trip per 2s.
func fetchThreatTable(mob string) *threatTableWire {
	threatTblMu.Lock()
	if threatTblMob == mob && time.Since(threatTblAt) < threatTblTTL {
		tbl := threatTbl
		threatTblMu.Unlock()
		return tbl
	}
	threatTblMu.Unlock()

	base := strings.TrimSuffix(serverURL, "/submit")
	u := base + "/threat?mob=" + url.QueryEscape(mob) + "&toon=" + url.QueryEscape(currentCharName)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	var tbl *threatTableWire
	if err == nil {
		if resp.StatusCode == http.StatusOK {
			var t threatTableWire
			if json.NewDecoder(resp.Body).Decode(&t) == nil {
				tbl = &t
				// The table carries the tuning — keep the collector in sync
				// for free while an officer is watching.
				storeThreatTuning(t.Config)
			}
		}
		resp.Body.Close()
	}
	threatTblMu.Lock()
	threatTbl, threatTblMob, threatTblAt = tbl, mob, time.Now()
	threatTblMu.Unlock()
	return tbl
}

// ThreatRowUI is one raider's row in the context list.
type ThreatRowUI struct {
	Name   string `json:"name"`
	Class  string `json:"class"`
	Threat int    `json:"threat"`
}

// ThreatMeterUI is everything the overlay renders.
type ThreatMeterUI struct {
	Officer   bool          `json:"officer"`
	Engaged   bool          `json:"engaged"`
	Mob       string        `json:"mob"`
	ElapsedS  int           `json:"elapsed_s"`
	DPS       float64       `json:"dps"`
	OwnThreat int           `json:"own_threat"`
	Tools     ThreatToolsUI `json:"tools"`
	// Gauge section — shown only while raid identification is live.
	RaidActive bool             `json:"raid_active"`
	HaveRef    bool             `json:"have_ref"`
	IsTank     bool             `json:"is_tank"`
	RefName    string           `json:"ref_name"`
	RefThreat  int              `json:"ref_threat"`
	TankSource string           `json:"tank_source"`
	Ratio      float64          `json:"ratio"`
	Zones      ThreatGaugeZones `json:"zones"`
	Others     []ThreatRowUI    `json:"others"`
}

// GetThreatMeter returns the overlay's full state. Officer-only by design:
// non-officers (and view-as personas — isOfficerCached already handles them)
// get {Officer:false} and the overlay shows its fallback line.
func (a *App) GetThreatMeter() ThreatMeterUI {
	out := ThreatMeterUI{Others: []ThreatRowUI{}}
	out.Zones = threatTuningCached().Gauge
	if !IsLinked() || !isOfficerCached() {
		return out
	}
	out.Officer = true

	tun := threatTuningCached()
	now := time.Now()
	threatMu.Lock()
	pruneThreatMobsLocked(now, tun)
	mt := threatMobs[threatCurrent]
	if mt != nil && now.Sub(mt.lastAct) <= threatDPSIdle(tun) {
		out.Engaged = true
		out.Mob = mt.display
		out.ElapsedS = threatElapsedLocked(mt, now, tun)
		out.DPS = threatDPSLocked(mt, now, tun)
		out.OwnThreat = int(mt.threat + 0.5)
	}
	out.Tools = threatTools
	threatMu.Unlock()
	if !out.Engaged {
		return out
	}

	tbl := fetchThreatTable(out.Mob)
	if tbl == nil {
		return out
	}
	out.RaidActive = tbl.RaidActive
	out.Zones = tbl.Config.Gauge
	out.TankSource = tbl.TankSource

	me := strings.ToLower(currentCharName)
	var ref *threatSnapWire
	if tbl.Tank != nil && strings.ToLower(tbl.Tank.Toon) == me {
		// The viewer IS the tank: compare against the closest rival, and a
		// comfortable tank wants that needle pegged deep in the red.
		out.IsTank = true
		if len(tbl.Others) > 0 {
			ref = &tbl.Others[0] // server sorts by threat descending
		}
	} else {
		ref = tbl.Tank
	}
	if ref != nil {
		out.HaveRef = true
		out.RefName = ref.Toon
		out.RefThreat = ref.Threat
		if ref.Threat > 0 {
			out.Ratio = float64(out.OwnThreat) / float64(ref.Threat)
			if cap := out.Zones.Cap; cap > 0 && out.Ratio > cap {
				out.Ratio = cap
			}
		} else if out.OwnThreat > 0 {
			out.Ratio = out.Zones.Cap
		}
	}
	for i, o := range tbl.Others {
		if i >= 3 {
			break
		}
		out.Others = append(out.Others, ThreatRowUI{Name: o.Toon, Class: o.Class, Threat: o.Threat})
	}
	return out
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DPS & Threat: parses the player's own combat lines into a per-mob damage
// and estimated-hate ledger. The damage parse opens and closes purely on
// damage matching — any melee fight, raid or not. Snapshots are relayed to
// the server (all linked clients; the tank may not be an officer), and the
// officer-only overlay compares the viewer's hate to the main tank's. Raid
// identification only gates the threat-gauge section, never the parse.

// ── tuning ───────────────────────────────────────────────────────────────────

// ThreatTuning mirrors the server's table (threatMeter.go); the raid-wide
// numbers live server-side so every client computes with identical values.
type ThreatTuning struct {
	ProcHate    int     `json:"proc_hate"`
	MissFactor  float64 `json:"miss_factor"`
	ProcWindowS int     `json:"proc_window_s"`
	// Swing threat = swing_dmg_mult × weaponDMG + main-hand damage bonus.
	// Sakuragi's Warrior Guide (P99 wiki, "The Mathematics of Threat
	// Generation") gives hate per swing as weapon DMG + damage bonus, landed
	// or missed — the damage actually rolled adds NOTHING for melee, which is
	// why the multiplier is 1.0. (Caster damage does generate hate; when
	// nukes/debuffs are modelled they get their own path, not this one.)
	// Weapon DMG comes from the equipped weapon, or is backed out of the
	// biggest non-crit plain swing seen (see threatSwingHate).
	WeaponDmgStart map[string]int `json:"weapon_dmg_start"`
	SwingDmgMult   float64        `json:"swing_dmg_mult"`
	// Same-skill dual wielders can't be split by verb; the offhand is assumed
	// equal to the primary scaled by this ratio.
	OffhandDmgRatio float64 `json:"offhand_dmg_ratio"`
	// Worn (item) haste for the base-delay estimate: -1 = detect from the
	// character's inventory file; >= 0 forces that percent. Item haste never
	// stacks — the inventory scan takes the single best worn item, never a sum.
	WornHastePct int `json:"worn_haste_pct"`
	// Dexterity assumed for a character whose procs we cannot observe, used
	// with the wiki's rate formula (threatAssumedProcsPerMin). Raiders run
	// capped dex, so 255 is the raid-realistic assumption.
	AssumedDex int `json:"assumed_dex"`
	// Successful Evades per minute assumed for a ROGUE we can't observe (their
	// evade lines are first-person and never reach us). 3.25 = one attempt
	// every 12s at a 65% success rate. Assuming zero is what made the model
	// rank 20 evading rogues above a tank who never lost aggro; set this to 0
	// to go back to that. Only applies to the unknown-character tier — our own
	// evades are counted from our own log, exactly.
	AssumedEvadePerMin float64 `json:"assumed_evade_per_min"`
	// Per-class max-hit multiplier: max non-crit hit ≈ DMG × mult + bonus.
	// Feeds the inference fallback ONLY — equipped-weapon pricing never
	// touches it, and it is no longer allowed to disprove gear either.
	// Calibrate from a known weapon: mult = (observed max − bonus) / DMG.
	MaxHitMult map[string]float64 `json:"max_hit_mult"`
	// Monk hands with no weapon equipped swing as fists.
	FistsDmg       int              `json:"fists_dmg"`
	FistsDelay     int              `json:"fists_delay"`
	SpecialHate    map[string]int   `json:"special_hate"`
	Reducers       map[string]int   `json:"reducers"`
	ReducerWindowS int              `json:"reducer_window_s"`
	Gauge          ThreatGaugeZones `json:"gauge"`
	DPSIdleResetS  int              `json:"dps_idle_reset_s"`
	// EngagedIdleS is how long after the last hate-generating action the
	// overlay still calls the fight live. Deliberately longer than the DPS
	// window, which models MELEE cadence: a caster acts every 30-60s, so
	// judging "are they in a fight" on a 20s swing gap blinks the meter off
	// between every cast — or never opens it at all for someone whose only
	// contribution is a debuff.
	EngagedIdleS     int `json:"engaged_idle_s"`
	ThreatIdleResetS int `json:"threat_idle_reset_s"`
	PostIntervalS    int `json:"post_interval_s"`
	ServerEntryTTLS  int `json:"server_entry_ttl_s"`
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
		// LAST-RESORT fallback: used only when the character has no readable
		// inventory file and no max-hit inference yet. Mid-tier values so the
		// error is bounded both ways (equipped-weapon pricing is the real path).
		WeaponDmgStart: map[string]int{
			"warrior": 12, "monk": 14, "rogue": 13, "ranger": 12,
			"paladin": 12, "shadow knight": 12, "bard": 10, "default": 10,
		},
		SwingDmgMult:       1.0,
		OffhandDmgRatio:    1.0,
		WornHastePct:       -1,
		AssumedDex:         255,
		AssumedEvadePerMin: 3.25,
		// Level-60 calibration, anchored on a 60 rogue whose BOTH weapons were
		// 15 dmg (Ragebringer / Axe of Resistance): mainhand max 96, offhand
		// max 85. (96−11)/15 = 85/15 = 5.667 — the two hands agree exactly,
		// and their difference is precisely the mainhand bonus. Sub-60
		// characters swing under a smaller damage table (a 49 ranger measured
		// 3.6), so inference under-reads their DMG; that is the fallback tier,
		// where a bounded error is the accepted trade (equipped-weapon pricing
		// needs no multiplier at all).
		MaxHitMult: map[string]float64{
			"warrior": 5.67, "rogue": 5.67, "monk": 5.67,
			"paladin": 5.37, "shadow knight": 5.37, "ranger": 5.37, "bard": 5.37,
			"default": 4.18,
		},
		FistsDmg:   9,
		FistsDelay: 16,
		// Backstab was 400 — a proc-sized spike — while a swing still cost
		// 2×DMG+bonus. Halving swing hate to Sakuragi's DMG+bonus doubled
		// backstab's relative weight without anyone deciding to: it became
		// 55-70% of a rogue's entire modelled hate. Field check: a 233s AoW
		// fight where the tank NEVER lost aggro (all 456 mob attacks on him)
		// while 20 rogues landed ~60 backstabs each. At 400 the model put 27
		// players above the tank (worst 212%); 250 is the highest value at
		// which no ROGUE out-models him — past that only monks remain, and
		// they Feign Death off the list where no log can follow.
		SpecialHate: map[string]int{
			"backstab": 250, "kick": 150, "flying_kick": 300, "bash": 200, "slam": 150,
		},
		Reducers: map[string]int{
			"concussion": 400, "jolt": 500, "cinder jolt": 500, "evade": 500,
		},
		ReducerWindowS:   6,
		Gauge:            ThreatGaugeZones{GreenMax: 0.70, YellowMax: 0.90, Cap: 1.50},
		DPSIdleResetS:    20,
		EngagedIdleS:     90,
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
	// The old client logs YOUR OWN proc/nuke damage with your character name,
	// not "You" — "Carboload hit a tigeraptor for 102 points of non-melee
	// damage." — so the subject is captured and checked against both forms.
	// Nearby raiders' procs print THEIR names and are excluded by the check.
	threatNonMeleeRE = regexp.MustCompile("^([\\w`' -]+?) hit (.+?) for (\\d+) points? of non-melee damage\\.$")
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
	// Crit announcements carry the damage; the hit line that follows repeats
	// it, and must not feed the weapon-DMG inference (crit ≈ 1.7×(max+5)
	// would wildly inflate it). YOUR OWN crits use the second-person form —
	// "You score a critical hit!" / "You land a Crippling Blow!" — while
	// nearby raiders' broadcast as "Name scores/lands ...".
	// Field format: "Carboload Scores a critical hit!(57)" — capitalized
	// verb, no space before the parens. Case-insensitive on the whole tail.
	threatCritRE = regexp.MustCompile(`^(\S+) (?i:scores? a critical hit!+|lands? a crippling blow!+) ?\((\d+)\)$`)
	// The TARGET named by a curated proc-effect line (filter.go's procPattern
	// matches the same shapes). A proc landing on a mob we aren't fighting is
	// somebody else's proc, full stop — field case: a rogue fighting Velketor
	// was credited for "Crystal Guardian is crushed by a wall of water."
	threatProcFxTargetRE = regexp.MustCompile("^([\\w`' -]+?)(?:'s (?:world dissolves|soul is consumed)| (?:is |grimaces|doubles over|begins to choke|has been|staggers back|sweats and shivers))")
	// Sourceless damage on a mob: our own spell landing, or our damage shield.
	// Which one it is depends on whether we have a cast in flight.
	threatNonMeleeAnonRE = regexp.MustCompile(`^(.+?) was hit by non-melee for (\d+) points? of damage\.$`)
	// A mob attacking US. These lines score nothing — the hate is the mob's
	// business, not ours — but they OPEN the engagement, which is what puts
	// the overlay on screen for a cleric being summoned or a puller eating
	// the first swing before anyone lands damage. "YOU" is only ever the
	// reader: other raiders' incoming hits print their names, never YOU.
	threatIncHitRE = regexp.MustCompile(`^(.+?) (?:hits|kicks|slashes|crushes|pierces|bashes|slams|strikes|punches|backstabs|bites|claws|smashes|slices|gores|mauls|rends|burns|stings|sweeps) YOU for (\d+) points? of damage\.$`)
	threatIncTryRE = regexp.MustCompile(`^(.+?) tries to (?:` + threatVerbs + `)(?: on)? YOU, but .+!$`)
	// Incoming spell damage. The line names no source, so it can only refresh
	// the current engagement or open the unnamed latch — never a ledger entry.
	threatIncNonMeleeRE = regexp.MustCompile(`^You were hit by non-melee for (\d+) points? of damage\.$`)
)

const (
	threatEvadeOk   = "You have momentarily ducked away from the main combat."
	threatEvadeFail = "Your attempts at ducking clear of combat fail."
)

// threatCastFailLines end an in-flight cast without it ever landing, so no
// hate is generated. Moving or ducking interrupts, taking a hit interrupts,
// a fizzle burns the cast, and a target out of range never receives it.
var threatCastFailLines = map[string]bool{
	"Your spell is interrupted.":               true,
	"Your casting has been interrupted!":       true,
	"Your spell fizzles!":                      true,
	"Your target is out of range, get closer!": true,
	"You can't cast spells while stunned!":     true,
	"You need to be standing to cast a spell.": true,
	"Your spell would not have taken hold.":    true,
}

// mobThreat is one mob's ledger entry. threat is a float so miss_factor can
// scale swings; it's rounded at the wire.
type mobThreat struct {
	display   string    // first-seen capitalization for the overlay header
	firstHit  time.Time // engagement start (log time)
	lastAct   time.Time
	lastMelee time.Time // gates the proc heuristic
	damage    int
	threat    float64
	// procs credited on this mob. Relayed so the server can price a tank's
	// procs from counted reality instead of the assumed rate.
	procs int
	// spellHate is the share of threat that came from casting rather than
	// swinging — it's what a debuffer or nuker generates, and the overlay
	// uses it to know a pure caster has hate worth showing without damage.
	spellHate int
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
	// Our in-flight NON-reducer cast (original case, for the DB lookup). The
	// damage and resist lines that follow name no caster, so this is the only
	// thing that makes caster hate attributable to us.
	threatPendCast   string
	threatPendCastAt time.Time
	// Mirrors "threatPendCast != """ for the cheap pre-lock gate: a debuff's
	// landing line is arbitrary flavour text ("Vyemm's movements slow.") that
	// matches none of the fixed shapes, so it can only be let through while we
	// actually have a cast in flight.
	threatCastArmed atomic.Bool
	// Spell-scoring diagnostics. A non-damaging debuff scores only if FIVE
	// things line up — the cast line parses, the name resolves in eq_spells,
	// it has a curated threat, cast_on_other yields a matcher, and a real log
	// line matches that matcher. When it doesn't score there is nothing to see,
	// so these record each step for the debug dump.
	threatCastLast    string // last non-reducer spell we saw ourselves begin
	threatCastLastAt  time.Time
	threatLandTried   []string // lines offered to the matcher while armed
	threatLandMatched string   // the line that scored, if one did
	threatTools       ThreatToolsUI
	// threatEngageLatch: combat proven by a line that names NO mob — incoming
	// spell damage ("You were hit by non-melee for ..."), or a spell resist
	// with no prior ledger entry. It opens the overlay without inventing a
	// ledger entry, so nothing here ever reaches the threat uplink or the
	// group DPS board; the first line that names the mob takes over.
	threatEngageLatch time.Time
	// Weapon-DMG inference, reset on character swap. Max hits and swing
	// cadence are tracked PER VERB so two weapons with different skills can
	// be told apart.
	threatInferChar string                        // character the inference state belongs to
	threatVerbMax   = map[string]*threatVerbHi{}  // verb → windowed biggest non-crit hit
	threatVerbCad   = map[string]*threatCadence{} // verb → swing cadence
	threatCritAmt   int                           // damage from our last crit announcement line
	threatCritAt    time.Time                     // when that announcement arrived
	// Active spell haste (worn haste comes from the inventory scan below).
	threatSpellHastePct int
	threatSpellHasteEnd time.Time
	// threatEquipStale latches gear fingerprints (char|primary|secondary)
	// that a tripwire disproved — swings that can't come from the equipped
	// weapons mean the inventory file predates a swap. The value is the
	// reason, kept for the debug dump. Cleared implicitly when the
	// fingerprint changes (a fresh /outputfile inventory or a char swap).
	threatEquipStale = map[string]string{}
	// threatLastFight is a snapshot of the most recently ended fight, for
	// the admin debug dump.
	threatLastFight threatFightSnap
	// Crit-announcement diagnostics for the debug dump: if the game's format
	// drifts from threatCritRE, the raw line shows exactly how.
	threatCritSeen    int
	threatCritHits    int
	threatCritLast    string
	threatCritLastHit bool
	// Proc-line diagnostics: what non-melee damage actually prints, so the
	// "procs don't register" question can be answered from a dump.
	threatProcSeen   int // first-person/name-form non-melee lines
	threatProcSelf   int // ...that attributed to this character
	threatProcLast   string
	threatProcSelfOK bool
	threatProc3p     int // broadcast "was hit by non-melee" lines (unattributable)
	threatProc3pLast string
	threatProcFx     int // curated proc-effect lines (procPattern) seen
	threatProcFxOK   int // ...that passed every gate and scored hate
	threatProcFxLast string
	threatProcFxSkip string // why the last one was not credited ("" = it was)
	// threatProcCreditAt debounces proc hate: a damage proc can print both
	// its effect line and a damage line — one credit per proc, not two.
	threatProcCreditAt time.Time
)

// threatFightSnap summarizes one ended engagement.
type threatFightSnap struct {
	Mob     string
	Dur     time.Duration
	Damage  int
	Threat  int
	EndedAt time.Time
}

// threatVerbHi is a two-window rolling maximum: the biggest non-crit hit of
// a verb over the last 10-20 minutes. A plain high-water mark never recovers
// from a weapon swap — an old bigger weapon would poison the DMG estimate
// for the rest of the session; with the window it ages out.
type threatVerbHi struct {
	cur, prev int
	rotated   time.Time // start of the current window
	// The most recent raise of cur. P99 prints the crit announcement AFTER
	// the hit line, so the forward skip can't stop a crit from poisoning
	// the max — the announcement retracts it instead (see retract).
	lastRaise   int
	lastRaiseAt time.Time
	curBefore   int
}

const threatMaxWindow = 10 * time.Minute

func (h *threatVerbHi) note(dmg int, at time.Time) {
	if h.rotated.IsZero() || at.Sub(h.rotated) > 2*threatMaxWindow {
		h.cur, h.prev, h.rotated = 0, 0, at
	} else if at.Sub(h.rotated) > threatMaxWindow {
		h.cur, h.prev, h.rotated = 0, h.cur, at
	}
	if dmg > h.cur {
		h.curBefore = h.cur
		h.cur = dmg
		h.lastRaise, h.lastRaiseAt = dmg, at
	}
}

// retract undoes the last max raise when a crit announcement names its
// damage — field data: a 60 warrior's crits (113, 141 on a 14-dmg weapon)
// fed the max before the announcement line arrived, inflating inferred DMG
// ~2.5x. The exact crit formula is still being calibrated; retraction works
// regardless, because it keys on the announced amount, not the formula.
func (h *threatVerbHi) retract(dmg int, at time.Time) bool {
	if h.lastRaise != dmg || h.cur != dmg || at.Sub(h.lastRaiseAt) > 2*time.Second {
		return false
	}
	h.cur = h.curBefore
	h.lastRaise = 0
	return true
}

func (h *threatVerbHi) maxAt(now time.Time) int {
	switch {
	case now.Sub(h.rotated) > 2*threatMaxWindow:
		return 0
	case now.Sub(h.rotated) > threatMaxWindow:
		return h.cur
	case h.prev > h.cur:
		return h.prev
	}
	return h.cur
}

// threatCadence collects the wall-clock ARRIVAL times of recent attack sets
// for one verb. The log stamps only whole seconds, but the tailer polls the
// file every 100ms, so live lines carry usable sub-second timing. A double/
// triple attack lands its 2-3 hits inside one set window and counts once.
type threatCadence struct {
	setStart time.Time
	times    []time.Time // attack-set times, oldest first, capped
}

const (
	threatSetWindow  = 150 * time.Millisecond // multi-attack grouping window
	threatCadMaxSets = 33                     // ~16 rounds per hand when dual
	threatCadSkew    = 3 * time.Second        // log time must track the wall clock
	threatCadMaxGap  = 5 * time.Second        // longer = downtime (XP roaming), restart the stream
)

func threatNoteSwingLocked(verb string, logAt, wallNow time.Time) {
	// Replayed backlog (startup rewind, alt-tab flush) arrives in one burst —
	// its arrival times say nothing about swing cadence.
	if d := wallNow.Sub(logAt); d < -threatCadSkew || d > threatCadSkew {
		return
	}
	c := threatVerbCad[verb]
	if c == nil {
		c = &threatCadence{}
		threatVerbCad[verb] = c
	}
	if !c.setStart.IsZero() && wallNow.Sub(c.setStart) <= threatSetWindow {
		return // double/triple attack: same swing set
	}
	c.setStart = wallNow
	if n := len(c.times); n > 0 && wallNow.Sub(c.times[n-1]) > threatCadMaxGap {
		c.times = c.times[:0]
	}
	c.times = append(c.times, wallNow)
	if len(c.times) > threatCadMaxSets {
		c.times = c.times[len(c.times)-threatCadMaxSets:]
	}
}

// threatStrideMedian returns the median of times[i+stride]-times[i] over the
// chain starting at `start`, in EQ delay units (tenths of a second); 0 until
// minDiffs samples exist. stride 1 reads a single timer; the two stride-2
// chains of an alternating two-timer stream are the two timers' own periods.
func threatStrideMedian(times []time.Time, start, stride, minDiffs int) float64 {
	var diffs []float64
	for i := start; i+stride < len(times); i += stride {
		diffs = append(diffs, times[i+stride].Sub(times[i]).Seconds()*10)
	}
	if len(diffs) < minDiffs {
		return 0
	}
	sort.Float64s(diffs)
	return diffs[len(diffs)/2]
}

// ── haste (P99 Haste Guide) ─────────────────────────────────────────────────

// Spell-haste buffs by their cast-on-you line. Quickness, Alacrity, Celerity
// and Swift Like the Wind all print the same line, so it maps to Celerity's
// 50% — the standard raid shaman haste. Durations are the at-cap values.
type threatHasteBuff struct {
	pct int
	dur time.Duration
}

var threatHasteLandings = map[string]threatHasteBuff{
	"You feel much faster.":                            {50, 16 * time.Minute},
	"You experience a quickening.":                     {64, 24 * time.Minute}, // Aanya's Quickening
	"Your body pulses with the spirit of the Shissar.": {66, 30 * time.Minute}, // Speed of the Shissar
	"You begin to move with wonderous rapidity.":       {70, 19 * time.Minute}, // Wonderous Rapidity
	"You experience visions of grandeur.":              {58, 42 * time.Minute}, // Visions of Grandeur
}

var threatHasteWearoffs = map[string]bool{
	"Your speed returns to normal.": true,
	"Your body slows.":              true, // Speed of the Shissar
	"Your visions fade.":            true, // Visions of Grandeur
}

// Worn haste items (item haste doesn't stack — the best one applies). Names
// as they appear in the inventory file, lowercased. From the P99 Haste
// Guide's item tables; weapons with permanent worn haste are included — the
// scan checks the Primary/Secondary rows like any other worn slot.
var threatWornItems = map[string]int{
	// belts
	"flowing red silk sash":      6,
	"swiftclaw sash":             15,
	"belt of concordance":        16,
	"belt of iniquity":           16,
	"belt of the pine":           16,
	"belt of virtue":             16,
	"swirlspine belt":            16,
	"belt of contention":         21,
	"belt of tranquility":        21,
	"belt of transience":         21,
	"flowing black silk sash":    21,
	"silvery belt of contention": 21,
	"sash of the dragonborn":     24,
	"honeycomb belt":             26,
	"runebranded girdle":         27,
	"runed bolster belt":         31,
	"girdle of rapidity":         31,
	"spiked seahorse hide belt":  34,
	"windraider's belt":          40,
	"sash of infinite blows":     41,
	"belt of the destroyer":      41,
	"belt of the four winds":     41,
	"feeliux's cord of velocity": 41,
	"girdle of dark power":       41,
	"girdle of faith":            41,
	"golden sash of tranquility": 41,
	"pegasus-hide belt":          41,
	"renard's belt of quickness": 41,
	// backs
	"siblisian berserker cloak":   26,
	"cloak of piety":              34,
	"shroud of the dar brood":     34,
	"cloak of flames":             36,
	"cloak of crystalline waters": 36,
	"rakusha cloak":               36,
	"cloak of the fire storm":     40,
	"dark cloak of the sky":       50,
	// hands
	"sporali gloves":              9,
	"silver chitin hand wraps":    22,
	"basoon haste gauntlets":      36,
	"gauntlets of dragon slaying": 41,
	// head
	"hangman's noose":         17,
	"cowl of mortality":       36,
	"brother xave's headband": 41,
	// feet
	"wyvern hide boots": 15,
	"grey suede boots":  41,
	// wrist / neck
	"silver bracelet of speed": 41,
	"yelinak's talisman":       41,
	// weapons with permanent worn haste
	"mithril two-handed sword":        31,
	"monsoon, sword of the swiftwind": 36,
	"typhoon, sword of the tidalwave": 36,
	"sunderfury":                      36,
	"velium swiftblade":               36,
	"ragebringer":                     40,
	"swiftwind":                       40,
	"tolan's longsword of the glade":  40,
	"claw of lightning":               41,
	"fist of lightning":               41,
	"rocksmasher":                     41,
	"serrated dragon tooth":           41,
}

// invScan is one pass over <Char>-Inventory.txt: equipped weapons + worn haste.
type invScan struct {
	found     bool
	wornPct   int
	primary   string // equipped weapon item names ("" = empty slot)
	secondary string
	modTime   time.Time // file mtime — /outputfile inventory freshness
}

var (
	invMu    sync.Mutex
	invCache invScan
	invChar  string
	invAt    time.Time
	invNote  string // last logged (char, gear, haste) state
)

// threatInventory returns the current character's cached inventory scan,
// re-read every 10 minutes (a small file). The file only changes when the
// player runs /outputfile inventory — staleness is handled by the gear
// tripwires, not here.
func threatInventory() invScan {
	name := currentCharName
	if name == "" {
		return invScan{}
	}
	invMu.Lock()
	defer invMu.Unlock()
	if invChar == name && time.Since(invAt) < 10*time.Minute {
		return invCache
	}
	invChar, invAt = name, time.Now()
	invCache = scanInventoryFile(name)
	// Surface the scan in the status log once per state — silent gear/haste
	// misses quietly skew the whole meter.
	note := fmt.Sprintf("%s|%v|%d|%s|%s", name, invCache.found, invCache.wornPct, invCache.primary, invCache.secondary)
	if note != invNote {
		invNote = note
		if !invCache.found {
			addStatus("DPS & Threat: no inventory file for %s — run /outputfile inventory in game to enable gear-based threat.", name)
		} else {
			p, s := invCache.primary, invCache.secondary
			if p == "" {
				p = "(empty)"
			}
			if s == "" {
				s = "(empty)"
			}
			addStatus("DPS & Threat: %s gear — primary %s · offhand %s · worn haste %d%%.", name, p, s, invCache.wornPct)
		}
	}
	return invCache
}

// threatAssumedProcsPerMin is the LAST-RESORT proc rate for a character whose
// procs nobody can see — no FuseBridge relaying their combat lines and no
// "/gu PROC - Target" macro. Sakuragi's Warrior Guide gives the game's own
// rate as 0.5 + 1.5×(dex/255) procs per minute in the mainhand, half that in
// the offhand. Order of data value, best first: bridge proc counting, macro
// counting, then this.
func threatAssumedProcsPerMin(tun ThreatTuning, dual bool) float64 {
	dex := tun.AssumedDex
	if dex <= 0 {
		dex = 255
	}
	if dex > 255 {
		dex = 255
	}
	main := 0.5 + 1.5*(float64(dex)/255)
	if !dual {
		return main
	}
	return main * 1.5 // mainhand + half-rate offhand
}

// threatWornHaste is the character's worn haste percent: the tuning override
// when set, else the best known haste item in the inventory scan. Worn haste
// items never stack, so this is a maximum and never a sum.
func threatWornHaste(tun ThreatTuning) int {
	if tun.WornHastePct >= 0 {
		return tun.WornHastePct
	}
	return threatInventory().wornPct
}

// scanInventoryFile reads <Char>-Inventory.txt (tab-separated: Location,
// Name, ...): the Primary/Secondary weapon names, plus the best worn-haste
// item among WORN slots — bag and bank locations are skipped.
func scanInventoryFile(name string) invScan {
	path := eqRootFilePath(GetSettings().EQDirectory, name+"-Inventory.txt")
	if path == "" {
		return invScan{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return invScan{}
	}
	sc := invScan{found: true}
	if st, err := os.Stat(path); err == nil {
		sc.modTime = st.ModTime()
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(f) < 2 {
			continue
		}
		loc, item := f[0], strings.TrimSpace(f[1])
		if item == "" || strings.EqualFold(item, "Empty") {
			continue
		}
		switch loc {
		case "Primary":
			sc.primary = item
		case "Secondary":
			sc.secondary = item
		}
		if loc == "Location" || strings.HasPrefix(loc, "General") ||
			strings.HasPrefix(loc, "Bank") || strings.HasPrefix(loc, "Shared") {
			continue
		}
		if v, ok := threatWornItems[strings.ToLower(item)]; ok && v > sc.wornPct {
			sc.wornPct = v
		}
	}
	return sc
}

// ── equipped-weapon stats (server item DB) ───────────────────────────────────

// threatWeaponInfo mirrors one /threat/weapons response entry. found = the
// item exists in the DB; known = it's a real weapon with dmg/delay (a shield
// is found but not known; an unscraped item is neither, and the server
// queues it for the wiki scraper).
type threatWeaponInfo struct {
	Name  string `json:"name"`
	Found bool   `json:"found"`
	Known bool   `json:"known"`
	Dmg   int    `json:"dmg"`
	Delay int    `json:"delay"`
	Skill string `json:"skill"`
}

var (
	weapMu    sync.Mutex
	weapCache = map[string]threatWeaponInfo{} // lower(name) → info
	weapTry   = map[string]time.Time{}        // last lookup attempt per name
)

// threatWeaponFor returns cached item stats, kicking off a background lookup
// when missing. Unresolved names retry every 5 minutes — the server queues
// unknowns for the wiki scraper, so they usually resolve within a few.
func threatWeaponFor(name string) (threatWeaponInfo, bool) {
	if strings.TrimSpace(name) == "" {
		return threatWeaponInfo{}, false
	}
	key := strings.ToLower(name)
	weapMu.Lock()
	info, ok := weapCache[key]
	tryDue := time.Since(weapTry[key]) > 5*time.Minute
	if tryDue {
		weapTry[key] = time.Now()
	}
	weapMu.Unlock()
	if (!ok || !info.Found) && tryDue && IsLinked() {
		go fetchThreatWeapons([]string{name})
	}
	return info, ok
}

func fetchThreatWeapons(names []string) {
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(struct {
		Names []string `json:"names"`
	}{names})
	req, err := http.NewRequest(http.MethodPost, base+"/threat/weapons", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
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
		Weapons []threatWeaponInfo `json:"weapons"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil {
		return
	}
	weapMu.Lock()
	for _, wi := range r.Weapons {
		weapCache[strings.ToLower(wi.Name)] = wi
	}
	weapMu.Unlock()
}

// threatWeaponVerb maps an item's weapon skill to its combat verb.
func threatWeaponVerb(skill string) string {
	s := strings.ToLower(skill)
	switch {
	case strings.Contains(s, "pierc"):
		return "pierce"
	case strings.Contains(s, "slash"):
		return "slash"
	case strings.Contains(s, "blunt"):
		return "crush"
	case strings.Contains(s, "hand to hand"):
		return "punch"
	}
	return ""
}

// threatEquipment is gear-based pricing for the current character.
type threatEquipment struct {
	ok        bool
	dual      bool
	main, off threatWeaponInfo
	mainVerb  string
	offVerb   string
	fp        string // char|primary|secondary fingerprint for the stale latch
}

// threatEquipmentLocked assembles gear pricing from the inventory scan and
// the item DB cache. ok=false falls back to max-hit inference: no inventory
// file, an unresolved item, or a tripwired (stale) gear set. Caller must
// hold threatMu.
func threatEquipmentLocked(tun ThreatTuning, class string) threatEquipment {
	inv := threatInventory()
	if !inv.found {
		return threatEquipment{}
	}
	eq := threatEquipment{fp: currentCharName + "|" + inv.primary + "|" + inv.secondary}
	if threatEquipStale[eq.fp] != "" {
		return threatEquipment{}
	}
	fists := threatWeaponInfo{Name: "fists", Found: true, Known: true,
		Dmg: tun.FistsDmg, Delay: tun.FistsDelay, Skill: "Hand to Hand"}
	switch {
	case inv.primary != "":
		wi, cached := threatWeaponFor(inv.primary)
		if !cached || !wi.Known {
			return threatEquipment{} // not resolved as a weapon (yet) — infer
		}
		eq.main = wi
	case class == "monk":
		eq.main = fists
	default:
		return threatEquipment{}
	}
	eq.mainVerb = threatWeaponVerb(eq.main.Skill)
	if strings.HasPrefix(strings.ToLower(eq.main.Skill), "2h") {
		eq.ok = true // two-hander: single-hand model even for dual classes
		return eq
	}
	if threatClassDual(class) {
		switch {
		case inv.secondary != "":
			wi, cached := threatWeaponFor(inv.secondary)
			switch {
			case cached && wi.Known:
				eq.off, eq.dual = wi, true
			case cached && wi.Found:
				// In the DB but not a weapon — a shield/held item: single-hand.
			default:
				return threatEquipment{} // can't tell shield from unscraped weapon — infer
			}
		case class == "monk":
			eq.off, eq.dual = fists, true
		}
	}
	if eq.dual {
		eq.offVerb = threatWeaponVerb(eq.off.Skill)
	}
	eq.ok = true
	return eq
}

// ── haste ────────────────────────────────────────────────────────────────────

// threatHasteCap is the max total haste by level (P99 Haste Guide).
func threatHasteCap(level int) int {
	switch {
	case level >= 60:
		return 100
	case level >= 55:
		return 94
	case level >= 51:
		return 84
	case level >= 31:
		return 74
	}
	return 50
}

var (
	threatRaidZoneMu sync.Mutex
	threatRaidZones  = map[string]bool{} // lowercased zones hosting a live raid
	threatRaidZoneAt time.Time
)

// storeThreatRaidZones records the server's live-raid zones, delivered on
// every threat snapshot POST.
func storeThreatRaidZones(zones []string) {
	m := make(map[string]bool, len(zones))
	for _, z := range zones {
		if z = strings.ToLower(strings.TrimSpace(z)); z != "" {
			m[z] = true
		}
	}
	threatRaidZoneMu.Lock()
	threatRaidZones, threatRaidZoneAt = m, time.Now()
	threatRaidZoneMu.Unlock()
}

// threatInLiveRaidZone: the player stands in a zone the server says hosts a
// live raid right now. The same zone during an XP session reports false —
// no raid, no assumption.
func threatInLiveRaidZone() bool {
	zone := strings.ToLower(strings.TrimSpace(CurrentZone()))
	if zone == "" {
		return false
	}
	threatRaidZoneMu.Lock()
	defer threatRaidZoneMu.Unlock()
	return time.Since(threatRaidZoneAt) < 5*time.Minute && threatRaidZones[zone]
}

// threatTotalHastePctLocked is the haste divided out of measured cadence.
// During a live raid in the player's zone, raiders are assumed fully buffed
// — at their level's haste cap; otherwise tracked worn + spell haste, capped.
func threatTotalHastePctLocked(tun ThreatTuning) int {
	level := levelForCurrentChar()
	if level <= 0 {
		level = 60
	}
	hasteCap := threatHasteCap(level)
	if threatInLiveRaidZone() {
		return hasteCap
	}
	total := threatWornHaste(tun)
	if threatSpellHastePct > 0 && time.Now().Before(threatSpellHasteEnd) {
		total += threatSpellHastePct
	}
	if total > hasteCap {
		total = hasteCap
	}
	return total
}

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

// threatTouchUnnamedLocked handles combat proof from a line that names no mob:
// refresh the current engagement when there is one, else open the unnamed
// latch so the overlay still appears — a wizard whose opener was resisted, or
// a cleric eating a nuke, is in a fight the ledger can't name yet. Returns
// whether the engagement view changed. Caller holds threatMu.
func threatTouchUnnamedLocked(at time.Time, tun ThreatTuning) bool {
	if mt := threatMobs[threatCurrent]; mt != nil {
		mt.lastAct = at
		return false
	}
	opened := threatEngageLatch.IsZero() || at.Sub(threatEngageLatch) > threatEngagedIdle(tun)
	threatEngageLatch = at
	return opened
}

// threatCurrentFight names the fight the overlay is on — the normalized
// ledger key and its display form — while the engagement is warm. The group
// DPS board keys off this so its rows describe the same fight as the header
// above them. Takes threatMu; never call it while holding rdpsMu.
func threatCurrentFight() (norm, display string, ok bool) {
	tun := threatTuningCached()
	now := time.Now()
	threatMu.Lock()
	defer threatMu.Unlock()
	mt := threatMobs[threatCurrent]
	if mt == nil || now.Sub(mt.lastAct) > threatEngagedIdle(tun) {
		return "", "", false
	}
	return threatCurrent, mt.display, true
}

// threatEngagedIdle is the window that decides whether the overlay shows at
// all. Separate from threatDPSIdle because the two answer different questions:
// the DPS window is "is this damage number still meaningful", which is about
// swing cadence, while this is "is this character in a fight". A shaman who
// lands a Malo and casts nothing else for a minute is very much in the fight
// and holds real hate for it.
func threatEngagedIdle(tun ThreatTuning) time.Duration {
	if tun.EngagedIdleS <= 0 {
		return 90 * time.Second
	}
	return time.Duration(tun.EngagedIdleS) * time.Second
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

// levelForCurrentChar reads the tailed character's cached level (server-fed
// characters.json cache, same source as class); 0 when unknown.
func levelForCurrentChar() int {
	name := strings.ToLower(strings.TrimSpace(currentCharName))
	if name == "" {
		return 0
	}
	charCacheMu.RLock()
	defer charCacheMu.RUnlock()
	return charCache[name].Level
}

// threatDamageBonus is the P99 main-hand 1H damage bonus: floor((level-25)/3)
// from level 28 on. Applied to every swing — the log can't tell main hand
// from offhand, so offhand swings are slightly overpriced. Game data, not
// tuning.
func threatDamageBonus(level int) int {
	if level < 28 {
		return 0
	}
	return (level - 25) / 3
}

// threatMaxHitMult is the level-60 max-damage multiplier by class: a max
// non-crit plain hit is roughly weaponDMG × mult + damage bonus, which is
// what lets us back the weapon's DMG out of the biggest hit seen. Owner-
// tunable (max_hit_mult) — live P99 max hits have run past the damage-table
// theory values, so calibrate against known weapons.
func threatMaxHitMult(class string, tun ThreatTuning) float64 {
	if v, ok := tun.MaxHitMult[class]; ok && v > 0 {
		return v
	}
	if v := tun.MaxHitMult["default"]; v > 0 {
		return v
	}
	switch class {
	case "warrior", "rogue", "monk":
		return 3.80
	case "paladin", "shadow knight", "ranger", "bard":
		return 3.60
	}
	return 2.80
}

// threatVerbIsSpecial: activated attacks with their own damage formulas —
// they use the tuned special table and never feed the weapon-DMG inference.
func threatVerbIsSpecial(verb string) bool {
	switch verb {
	case "backstab", "kick", "bash", "slam":
		return true
	}
	return false
}

// threatClassHasBonus: melee classes get the primary-slot damage bonus table
// (the wiki's "same for all melee classes"); pure casters/priests don't.
func threatClassHasBonus(class string) bool {
	switch class {
	case "warrior", "rogue", "monk", "ranger", "bard", "paladin", "shadow knight":
		return true
	}
	return false
}

// threatClassDual: classes that dual wield at raid level, so their swings mix
// a bonused primary with an unbonused offhand.
func threatClassDual(class string) bool {
	switch class {
	case "warrior", "rogue", "monk", "ranger", "bard":
		return true
	}
	return false
}

// threatClassPureMelee: classes with no spellbook at all. Any "Your target
// resisted the X spell." in their log can only be a weapon proc resisting —
// and a resisted proc generates full hate (P99 wiki: resisted spells aggro
// like landed ones).
func threatClassPureMelee(class string) bool {
	switch class {
	case "warrior", "rogue", "monk":
		return true
	}
	return false
}

// threatClassTank: the classes that carry the proc weapons behind the curated
// effect lines (filter.go's procPattern — the guild's tank proc list). Those
// lines name only the target, so class is the one attribution filter we have.
func threatClassTank(class string) bool {
	switch class {
	case "warrior", "paladin", "shadow knight":
		return true
	}
	return false
}

// ── caster hate: spells priced by base damage ────────────────────────────────

// threatSpellInfo mirrors one /threat/spells entry. Found = the spell is in
// the DB; Known = somebody has curated its threat value.
type threatSpellInfo struct {
	Name        string `json:"name"`
	Found       bool   `json:"found"`
	Known       bool   `json:"known"`
	Threat      int    `json:"threat"`
	CastTime    string `json:"cast_time"`
	SpellType   string `json:"spell_type"`
	CastOnOther string `json:"cast_on_other"`
}

// threatPlaceholders are the stand-ins a wiki page uses for the spell's target.
// The P99 pages overwhelmingly write "Someone" ("Someone's movements slow.");
// "Soandso" is the Lucy/live-EQ convention and turns up on imported pages. Both
// have to be recognised — a template whose placeholder isn't matched yields no
// matcher at all, and a debuff with no matcher can never be seen to land.
var threatPlaceholders = []string{"someone", "soandso"}

// threatLandRE turns a wiki "Cast on Other" template into a matcher for the
// real log line, capturing the target's name. The placeholder becomes the
// capture; everything else is matched literally.
//
// This is the ONLY way a non-damaging debuff can be seen to land: a slow or a
// malo prints no damage line at all, so without it those spells would generate
// no hate and their casters would read as doing nothing.
func threatLandRE(template string) *regexp.Regexp {
	// Scrape artifacts: the wiki's markup leaves stray whitespace, and the
	// possessive in particular arrives as "Someone 's skin turns hard as wood."
	// where the game prints "Bobmage's skin turns hard as wood." Left alone,
	// that lone space makes the pattern unmatchable by any real log line.
	t := strings.Join(strings.Fields(template), " ")
	t = strings.ReplaceAll(t, " '", "'")
	if t == "" {
		return nil
	}
	lower := strings.ToLower(t)
	for _, ph := range threatPlaceholders {
		i := strings.Index(lower, ph)
		if i < 0 {
			continue
		}
		pat := "^" + regexp.QuoteMeta(t[:i]) + "(.+?)" + regexp.QuoteMeta(t[i+len(ph):]) + "$"
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil
		}
		return re
	}
	return nil // no target placeholder — can't tell whose it was
}

var (
	landREMu sync.Mutex
	landRE   = map[string]*regexp.Regexp{} // template → compiled matcher
)

func threatLandMatcher(template string) *regexp.Regexp {
	landREMu.Lock()
	defer landREMu.Unlock()
	if re, ok := landRE[template]; ok {
		return re
	}
	re := threatLandRE(template)
	landRE[template] = re
	return re
}

var (
	spellCacheMu sync.Mutex
	spellCache   = map[string]threatSpellInfo{} // lower(name) → info
	spellTry     = map[string]time.Time{}       // lower(name) → last fetch attempt
	// Spells seen cast that carry no curated threat value — surfaced in the
	// debug dump so the ratings list can be filled in from real raids.
	spellUnrated = map[string]int{}
)

const spellRetry = 10 * time.Minute

// threatSpellFor returns the cached rating for a spell, kicking off a lookup
// when it's missing. cached=false means "ask again later", never "worth zero".
func threatSpellFor(name string) (threatSpellInfo, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return threatSpellInfo{}, false
	}
	spellCacheMu.Lock()
	info, ok := spellCache[key]
	last := spellTry[key]
	if !ok && time.Since(last) > spellRetry {
		spellTry[key] = time.Now()
		spellCacheMu.Unlock()
		go fetchThreatSpells([]string{name})
		return threatSpellInfo{}, false
	}
	spellCacheMu.Unlock()
	return info, ok
}

func fetchThreatSpells(names []string) {
	if !IsLinked() || len(names) == 0 {
		return
	}
	body, _ := json.Marshal(struct {
		Names []string `json:"names"`
	}{names})
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodPost, base+"/threat/spells", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var out struct {
		Spells []threatSpellInfo `json:"spells"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return
	}
	spellCacheMu.Lock()
	for _, s := range out.Spells {
		spellCache[strings.ToLower(s.Name)] = s
	}
	spellCacheMu.Unlock()
}

// threatSpellWindow is how long after "You begin casting X." a damage or
// resist line still belongs to that cast: the spell's own cast time plus
// slack for travel and server lag. Long nukes cast for 8-10s, so a fixed
// short window would drop exactly the spells worth the most hate.
func threatSpellWindow(info threatSpellInfo) time.Duration {
	secs := 0.0
	if f := strings.TrimSpace(strings.TrimSuffix(info.CastTime, "s")); f != "" {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			secs = v
		}
	}
	return time.Duration((secs + 8) * float64(time.Second)) // slack for travel/lag
}

// threatBonusNowLocked is the current character's mainhand damage bonus (0
// for classes that don't get one). Caller must hold threatMu.
func threatBonusNowLocked() int {
	class := strings.ToLower(strings.TrimSpace(classForCurrentChar()))
	if !threatClassHasBonus(class) {
		return 0
	}
	level := levelForCurrentChar()
	if level <= 0 {
		level = 60
	}
	return threatDamageBonus(level)
}

// threatSwingModel is the live two-hand swing model. The log never says which
// hand a hit came from, so the hands are separated by VERB when the two
// weapons use different skills (slash mainhand / pierce offhand), and assumed
// equal — scaled by offhand_dmg_ratio — when they share one. The offhand
// never gets the damage bonus.
type threatSwingModel struct {
	DmgMain, DmgOff     float64
	Bonus               int
	TPSMain, TPSOff     float64 // TPSOff meaningful only when Dual
	DelayMain, DelayOff float64 // estimated BASE delay, EQ units; 0 = not enough data
	HastePct            int     // total haste divided out of the delay estimate
	Dual                bool
	MainVerb, OffVerb   string // set only when two verbs separate the hands
	Src                 string // "gear" (equipped weapons) or "inferred" (max hits)
}

// threatSwingModelLocked builds the swing model. Gear-first: equipped
// weapons resolved through the inventory file and item DB price the hands
// exactly. When gear can't be used (no file, unresolved item, tripwired
// stale) it falls back to max-hit inference: weapon DMG starts at the class
// assumption and rises when the biggest non-crit hit of a verb implies a
// better weapon. Caller must hold threatMu.
func threatSwingModelLocked(tun ThreatTuning) threatSwingModel {
	class := strings.ToLower(strings.TrimSpace(classForCurrentChar()))
	level := levelForCurrentChar()
	if level <= 0 {
		level = 60 // roster hasn't resolved yet — assume a raid-level character
	}
	var m threatSwingModel
	if threatClassHasBonus(class) {
		m.Bonus = threatDamageBonus(level)
	}
	m.Dual = threatClassDual(class)

	tp := tun.SwingDmgMult
	if tp <= 0 {
		tp = 1.0
	}
	if eq := threatEquipmentLocked(tun, class); eq.ok {
		m.Src = "gear"
		m.HastePct = threatTotalHastePctLocked(tun)
		m.Dual = eq.dual
		m.DmgMain = float64(eq.main.Dmg)
		m.DelayMain = float64(eq.main.Delay)
		m.TPSMain = tp*m.DmgMain + float64(m.Bonus)
		if eq.dual {
			m.DmgOff = float64(eq.off.Dmg)
			m.DelayOff = float64(eq.off.Delay)
			m.TPSOff = tp * m.DmgOff
			if eq.mainVerb != eq.offVerb {
				m.MainVerb, m.OffVerb = eq.mainVerb, eq.offVerb
			}
			// Same skill both hands: verbs stay empty, so swings price at
			// the two hands' average — exact, since both DMG are known.
		}
		return m
	}
	m.Src = "inferred"

	start := float64(tun.WeaponDmgStart["default"])
	if v, ok := tun.WeaponDmgStart[class]; ok && class != "" {
		start = float64(v)
	}
	// Top two verbs by windowed max non-crit hit. The bigger belongs to the
	// primary (its max carries the bonus), the smaller to the offhand.
	now := time.Now()
	v1, max1, v2, max2 := "", 0, "", 0
	for verb, hi := range threatVerbMax {
		mx := hi.maxAt(now)
		switch {
		case mx == 0:
		case mx > max1:
			v2, max2 = v1, max1
			v1, max1 = verb, mx
		case mx > max2:
			v2, max2 = verb, mx
		}
	}
	mult := threatMaxHitMult(class, tun)
	m.DmgMain = start
	if max1 > 0 {
		if inferred := (float64(max1) - float64(m.Bonus)) / mult; inferred > m.DmgMain {
			m.DmgMain = inferred
		}
	}
	m.TPSMain = tp*m.DmgMain + float64(m.Bonus)
	if m.Dual {
		ratio := tun.OffhandDmgRatio
		if ratio <= 0 {
			ratio = 1.0
		}
		m.DmgOff = m.DmgMain * ratio
		if v2 != "" {
			// Two skills in play: the offhand gets its own inference (no
			// bonus on its max) instead of the ratio assumption.
			m.MainVerb, m.OffVerb = v1, v2
			m.DmgOff = start * ratio
			if inferred := float64(max2) / mult; inferred > m.DmgOff {
				m.DmgOff = inferred
			}
		}
		m.TPSOff = tp * m.DmgOff
	}

	// Estimated BASE weapon delay: measured attack-set cadence with total
	// haste multiplied back in (hasted = base/(1+haste)).
	m.HastePct = threatTotalHastePctLocked(tun)
	factor := 1 + float64(m.HastePct)/100
	stride1 := func(verb string) float64 {
		if c := threatVerbCad[verb]; c != nil {
			return threatStrideMedian(c.times, 0, 1, 4) * factor
		}
		return 0
	}
	if m.OffVerb != "" {
		// Different skills: each verb is one weapon's own timer.
		m.DelayMain = stride1(m.MainVerb)
		m.DelayOff = stride1(m.OffVerb)
	} else if v1 != "" {
		if m.Dual {
			// Same skill on both hands: with near-equal delays the hands
			// alternate, so the two stride-2 chains are the two weapons'
			// own periods (equal chains = matched weapons). Which chain is
			// which hand is unknowable — the faster one is shown as the
			// primary.
			if c := threatVerbCad[v1]; c != nil {
				a := threatStrideMedian(c.times, 0, 2, 3)
				b := threatStrideMedian(c.times, 1, 2, 3)
				if a > 0 && b > 0 {
					if a > b {
						a, b = b, a
					}
					m.DelayMain, m.DelayOff = a*factor, b*factor
				}
			}
		} else {
			m.DelayMain = stride1(v1)
		}
	}
	return m
}

// threatSwingHate maps one landed/attempted swing to hate: activated attacks
// (backstab, kick, bash, slam) use the special table — monks' Flying Kick
// logs as plain "kick", so their kick maps to flying_kick — and everything
// else is a plain swing priced by the two-hand weapon model. A swing whose
// verb pins it to a hand gets that hand's price; an ambiguous one (both
// weapons share the skill) gets the two hands' average. Caller must hold
// threatMu.
func threatSwingHate(verb string, tun ThreatTuning) float64 {
	switch verb {
	case "backstab":
		return float64(tun.SpecialHate["backstab"])
	case "kick":
		if strings.EqualFold(strings.TrimSpace(classForCurrentChar()), "monk") {
			return float64(tun.SpecialHate["flying_kick"])
		}
		return float64(tun.SpecialHate["kick"])
	case "bash":
		return float64(tun.SpecialHate["bash"])
	case "slam":
		return float64(tun.SpecialHate["slam"])
	}
	m := threatSwingModelLocked(tun)
	if !m.Dual {
		return m.TPSMain
	}
	if m.OffVerb != "" {
		if verb == m.MainVerb {
			return m.TPSMain
		}
		if verb == m.OffVerb {
			return m.TPSOff
		}
	}
	return (m.TPSMain + m.TPSOff) / 2
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
	// The curated tank-weapon proc-effect lines (same list as the Fuse "Proc
	// Counter" trigger and the proc-forwarding filter, filter.go).
	procEffect := procPattern.MatchString(content)
	// A debuff's landing line is free-form flavour text, so it clears the gate
	// below only while a cast is actually in flight.
	castArmed := threatCastArmed.Load()
	// "ritical"/"rippling" sidestep the game's inconsistent capitalization
	// ("Scores a critical hit!" arrived with a capital S in the field).
	if !procEffect && !castArmed &&
		!strings.HasPrefix(content, "You") &&
		// " YOU " admits a mob's attacks on the reader — "X hits YOU for ..."
		// / "X tries to hit YOU, but ..." — which open the engagement. The
		// miss form puts a comma straight after YOU, hence the second probe.
		!strings.Contains(content, " YOU ") &&
		!strings.Contains(content, " YOU,") &&
		!strings.Contains(content, " has been slain by ") &&
		!strings.HasSuffix(content, "of non-melee damage.") &&
		!strings.Contains(content, " was hit by non-melee for ") &&
		!strings.HasSuffix(content, "staggers from a blow to the head.") &&
		!strings.HasSuffix(content, "s head snaps back.") &&
		!strings.Contains(content, "ritical hit!") &&
		!strings.Contains(content, "rippling Blow!") &&
		!strings.Contains(content, "rippling blow!") {
		return
	}
	// The log's own timestamp, not the wall clock: the tailer can replay a
	// burst of historical combat at startup (randoms.go has the long story).
	at := logLineTime(line)
	wallNow := time.Now()
	if at.IsZero() {
		at = wallNow
	}
	tun := threatTuningCached()

	changed := false
	threatMu.Lock()
	// A character swap invalidates everything: the ledger, the reducer
	// counters, and especially the weapon-DMG inference.
	if threatInferChar != currentCharName {
		threatInferChar = currentCharName
		threatVerbMax = map[string]*threatVerbHi{}
		threatVerbCad = map[string]*threatCadence{}
		threatCritAmt = 0
		threatSpellHastePct = 0
		threatMobs = map[string]*mobThreat{}
		threatCurrent, threatPendSpell = "", ""
		threatTools = ThreatToolsUI{}
		threatProcCreditAt = time.Time{}
		threatEngageLatch = time.Time{}
	}
	pruneThreatMobsLocked(at, tun)

	switch {
	// Non-melee before melee: RE2 has no lookahead (see grammar note above).
	case strings.HasSuffix(content, "of non-melee damage."):
		threatProcSeen++
		threatProcLast = content
		threatProcSelfOK = false
		if m := threatNonMeleeRE.FindStringSubmatch(content); m != nil &&
			(m[1] == "You" || strings.EqualFold(m[1], currentCharName)) {
			threatProcSelf++
			threatProcSelfOK = true
			dmg, _ := strconv.Atoi(m[3])
			norm := normThreatMob(m[2])
			mt, ch := threatEnsureLocked(norm, m[2], at, tun)
			changed = ch
			mt.damage += dmg
			// A weapon proc can only come off a swing; a non-melee hit with
			// no recent melee is a (out-of-scope) nuke — damage only. The
			// debounce shares one credit with the proc's effect line.
			if !mt.lastMelee.IsZero() && at.Sub(mt.lastMelee) <= time.Duration(tun.ProcWindowS)*time.Second &&
				at.Sub(threatProcCreditAt) > 1500*time.Millisecond {
				mt.threat += float64(tun.ProcHate)
				mt.procs++
				threatProcCreditAt = at
			}
			mt.lastAct = at
		}

	// " of damage." covers both "points" and the singular "1 point"; the
	// non-melee line ends "non-melee damage." and can't reach this case.
	//
	// threatMeleeHitRE is anchored on "You", so this whole parser is
	// first-person by construction — which is also what keeps PETS out of
	// threat. Raid DPS deliberately counts them (a charmed giant can be the
	// top damage row), but hate is a per-character estimate the viewer is
	// trying to stay under, and a pet's aggro is neither theirs to spend nor
	// theirs to shed. Nothing here needs to filter for that; a pet's swings
	// are third-person and never match.
	case strings.HasSuffix(content, " of damage."):
		if m := threatMeleeHitRE.FindStringSubmatch(content); m != nil {
			dmg, _ := strconv.Atoi(m[3])
			norm := normThreatMob(m[2])
			mt, ch := threatEnsureLocked(norm, m[2], at, tun)
			changed = ch
			mt.damage += dmg
			// Feed the weapon-DMG inference before pricing the swing: plain
			// swings only, and never the hit a crit announcement just named.
			// The generic "hit" verb (thrown weapons, oddball held items)
			// carries no hand information — it must never feed inference or
			// claim the offhand slot. Its damage and threat still count.
			if !threatVerbIsSpecial(m[1]) && m[1] != "hit" {
				// A crit hit must never feed the max: the announcement can
				// arrive before the hit (skip it here) or after it (retracted
				// in the crit case). Mainhand crits read as announce+bonus.
				crit := threatCritAmt > 0 && at.Sub(threatCritAt) <= 2*time.Second &&
					(dmg == threatCritAmt || dmg == threatCritAmt+threatBonusNowLocked())
				if crit {
					threatCritAmt = 0
				} else {
					hi := threatVerbMax[m[1]]
					if hi == nil {
						hi = &threatVerbHi{}
						threatVerbMax[m[1]] = hi
					}
					hi.note(dmg, at)
				}
				threatGearTripwireLocked(m[1], tun)
				threatNoteSwingLocked(m[1], at, wallNow)
			}
			mt.threat += threatSwingHate(m[1], tun)
			mt.lastMelee = at
			mt.lastAct = at
		} else if m := threatIncHitRE.FindStringSubmatch(content); m != nil {
			// A mob hitting US shares this suffix. Engagement only: it names
			// the fight (and becomes the current one — the thing beating on
			// you IS your fight), but none of its numbers are ours to score.
			norm := normThreatMob(m[1])
			mt, ch := threatEnsureLocked(norm, m[1], at, tun)
			changed = ch
			mt.lastAct = at
		} else if threatIncNonMeleeRE.MatchString(content) {
			// Nuked by the mob. No source in the line, so this can only
			// refresh the current fight or open the unnamed latch.
			if threatTouchUnnamedLocked(at, tun) {
				changed = true
			}
		}

	case strings.Contains(content, " tries to ") && strings.Contains(content, " YOU, but "):
		if m := threatIncTryRE.FindStringSubmatch(content); m != nil {
			// A mob swinging at us and missing still proves the fight.
			norm := normThreatMob(m[1])
			mt, ch := threatEnsureLocked(norm, m[1], at, tun)
			changed = ch
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
			if !threatVerbIsSpecial(m[1]) && m[1] != "hit" {
				threatGearTripwireLocked(m[1], tun)
				threatNoteSwingLocked(m[1], at, wallNow)
			}
			mt.lastMelee = at
			mt.lastAct = at
		}

	case strings.Contains(content, " was hit by non-melee for "):
		// This line names no source. A character only ever sees it for damage
		// THEY caused — their own damage shield, or their own spell landing —
		// so the discriminator is whether we have a cast in flight:
		//
		//   cast in flight → our nuke landed. Hate is the spell's BASE value
		//                    from the ratings table, never the damage rolled.
		//   no cast        → a damage shield. Real damage, but zero hate, so
		//                    the ledger must not move.
		threatProc3p++
		threatProc3pLast = content
		if m := threatNonMeleeAnonRE.FindStringSubmatch(content); m != nil && threatPendCast != "" {
			if info, ok := threatSpellFor(threatPendCast); ok &&
				at.Sub(threatPendCastAt) <= threatSpellWindow(info) {
				dmg, _ := strconv.Atoi(m[2])
				norm := normThreatMob(m[1])
				mt, ch := threatEnsureLocked(norm, m[1], at, tun)
				changed = ch
				mt.damage += dmg
				if info.Known {
					mt.threat += float64(info.Threat)
					mt.spellHate += info.Threat
				} else {
					spellUnrated[info.Name]++
				}
				mt.lastAct = at
				threatPendCast = ""
				threatCastArmed.Store(false)
			}
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

	case threatCastFailLines[content]:
		// The cast never completed, so it never touched the hate list: moving
		// or ducking interrupts it, a fizzle wastes it, an out-of-range target
		// never gets it. Disarm, or the NEXT damage line on that mob (someone
		// else's, or our own damage shield) would be scored as our spell.
		threatPendCast = ""
		threatCastArmed.Store(false)
		threatPendSpell = ""

	case strings.HasPrefix(content, "You begin casting "):
		if m := threatCastRE.FindStringSubmatch(content); m != nil {
			raw := strings.TrimSpace(m[1])
			spell := strings.ToLower(raw)
			if _, ok := tun.Reducers[spell]; ok && spell != "evade" {
				threatPendSpell, threatPendAt = spell, at
				break
			}
			// Any other cast is a candidate for caster hate. The outcome line
			// that follows — damage on the target, or a resist — is what
			// actually scores it; arming here is what makes those lines
			// attributable at all, since neither names the caster.
			// Spell Info off: don't arm at all, so no cast is priced and no
			// spell hate is estimated or reported. Melee hate is unaffected.
			if !GetSettings().SpellInfo {
				break
			}
			threatPendCast, threatPendCastAt = raw, at
			threatCastArmed.Store(true)
			threatCastLast, threatCastLastAt = raw, at
			threatLandTried, threatLandMatched = nil, ""
			threatSpellFor(raw) // warm the rating cache for the outcome line
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
			} else if threatPendCast != "" && strings.EqualFold(spell, threatPendCast) {
				// A resisted spell still generates its FULL hate — that's the
				// classic way a caster pulls aggro off a tank having done no
				// damage at all. No damage is added, only hate.
				if info, ok := threatSpellFor(threatPendCast); ok &&
					at.Sub(threatPendCastAt) <= threatSpellWindow(info) {
					if mt := threatMobs[threatCurrent]; mt != nil && info.Known {
						mt.threat += float64(info.Threat)
						mt.spellHate += info.Threat
						mt.lastAct = at
					} else if !info.Known {
						spellUnrated[info.Name]++
					}
				}
				threatPendCast = ""
				threatCastArmed.Store(false)
			} else if threatClassPureMelee(strings.ToLower(strings.TrimSpace(classForCurrentChar()))) {
				// No spellbook — this resist is a weapon proc resisting,
				// which aggros like a landed proc.
				if mt := threatMobs[threatCurrent]; mt != nil {
					mt.threat += float64(tun.ProcHate)
					mt.procs++
					mt.lastAct = at
				}
			}
			// However it was scored, a resist is proof of a fight: refresh
			// the current engagement, or — a wizard whose opener was
			// resisted, with no ledger entry to hang it on — open the
			// unnamed latch so the overlay appears anyway.
			if threatTouchUnnamedLocked(at, tun) {
				changed = true
			}
		}

	case content == "Your target is immune to changes in run speed." ||
		content == "Your target is immune to changes in its run speed." ||
		content == "Your spell did not take hold.":
		// The proc-forwarding filter's other resist shapes. For a class with
		// no spellbook these are proc outcomes too — but "did not take hold"
		// also prints for failed buff clickies, so it only counts while
		// actively meleeing.
		//
		// The snare-immune line really reads "...changes in ITS run speed" on
		// P99; matching only the possessive-less form scored zero for every
		// aggro clicky ever dumped (a tank's opener is several of these).
		if threatClassPureMelee(strings.ToLower(strings.TrimSpace(classForCurrentChar()))) {
			if mt := threatMobs[threatCurrent]; mt != nil {
				if content != "Your spell did not take hold." ||
					(!mt.lastMelee.IsZero() && at.Sub(mt.lastMelee) <= time.Duration(tun.ProcWindowS)*time.Second) {
					mt.threat += float64(tun.ProcHate)
					mt.procs++
					mt.lastAct = at
				}
			}
		}

	case procEffect:
		// A curated proc-effect line names the TARGET, never the caster, so
		// crediting it takes three gates: the line must name the mob we are
		// fighting, we must be swinging at it (inside the proc window), and it
		// is debounced against the damage line the same proc may also print.
		//
		// The curated list is the guild's TANK proc list (the same lines the
		// Fuse "Proc Counter" trigger watches), so only a tank class can be
		// its source. Without that gate a raid's tank procs land on whoever
		// happens to be parsing: a 60 rogue's dump showed 12 credited effect
		// lines — ~4,800 hate, 13% of that fight — with zero procs of his own.
		// A non-tank holding a proc weapon still gets credit from the
		// first-person damage line and (pure melee) from resists.
		threatProcFx++
		threatProcFxLast = content
		fxTarget := ""
		if m := threatProcFxTargetRE.FindStringSubmatch(content); m != nil {
			fxTarget = normThreatMob(m[1])
		}
		mt := threatMobs[threatCurrent]
		switch {
		case !threatClassTank(strings.ToLower(strings.TrimSpace(classForCurrentChar()))):
			threatProcFxSkip = "not a tank class — curated proc lines belong to tank weapons"
		case fxTarget == "" || fxTarget != threatCurrent:
			threatProcFxSkip = "names another mob (" + fxTarget + ")"
		case mt == nil || mt.lastMelee.IsZero() ||
			at.Sub(mt.lastMelee) > time.Duration(tun.ProcWindowS)*time.Second:
			threatProcFxSkip = "no swing on that mob inside the proc window"
		case at.Sub(threatProcCreditAt) <= 1500*time.Millisecond:
			threatProcFxSkip = "debounced against the same proc's damage line"
		default:
			threatProcFxSkip = ""
			threatProcFxOK++
			mt.threat += float64(tun.ProcHate)
			mt.procs++
			mt.lastAct = at
			threatProcCreditAt = at
		}

	case strings.HasSuffix(content, "staggers from a blow to the head."):
		threatReducerLandedLocked(content, threatConcOkRE, "concussion", at, tun)

	case strings.HasSuffix(content, "s head snaps back."):
		threatReducerLandedLocked(content, threatJoltOkRE, "jolt", at, tun)

	case strings.Contains(content, "ritical hit!") || strings.Contains(content, "rippling Blow!") ||
		strings.Contains(content, "rippling blow!"):
		// Only our own announcements matter — other raiders' crits broadcast
		// too, and their damage lines never match ^You anyway. Self crits
		// say "You", broadcast crits say the character name.
		threatCritSeen++
		threatCritLast = content
		threatCritLastHit = false
		if m := threatCritRE.FindStringSubmatch(content); m != nil &&
			(m[1] == "You" || strings.EqualFold(m[1], currentCharName)) {
			threatCritHits++
			threatCritLastHit = true
			amt, _ := strconv.Atoi(m[2])
			// Arm the forward skip (covers announcement-first ordering)...
			threatCritAmt, threatCritAt = amt, at
			// ...and retract what the crit already poisoned (P99 prints the
			// announcement AFTER the hit line).
			//
			// The announced number is the crit roll BEFORE the mainhand damage
			// bonus: an AoW raid log paired three announcements with the hit
			// that followed at exactly announce+11 (86→97, 52→63, 32→43). So a
			// mainhand crit lands in the max as amt+bonus and an offhand crit
			// (no bonus) as amt — try both, or mainhand crits never retract and
			// keep inflating the inferred weapon.
			for _, hi := range threatVerbMax {
				if hi.retract(amt, at) {
					continue
				}
				if b := threatBonusNowLocked(); b > 0 {
					hi.retract(amt+b, at)
				}
			}
		}

	case strings.HasPrefix(content, "You have slain "):
		if m := threatYouSlainRE.FindStringSubmatch(content); m != nil {
			changed = threatMobDiedLocked(normThreatMob(m[1]))
		}

	default:
		if hb, ok := threatHasteLandings[content]; ok {
			// A haste change invalidates the cadence measured so far — the
			// delay estimate must come from one haste regime.
			if threatSpellHastePct != hb.pct {
				threatVerbCad = map[string]*threatCadence{}
			}
			threatSpellHastePct, threatSpellHasteEnd = hb.pct, at.Add(hb.dur)
		} else if threatHasteWearoffs[content] {
			if threatSpellHastePct != 0 {
				threatVerbCad = map[string]*threatCadence{}
			}
			threatSpellHastePct = 0
		} else if m := threatSlainRE.FindStringSubmatch(content); m != nil {
			changed = threatMobDiedLocked(normThreatMob(m[1]))
		} else if threatPendCast != "" {
			// A debuff landing. It prints only the spell's own flavour text and
			// names the target, so this both scores the hate and tells us which
			// mob to score it on — a slower may never have swung at it.
			//
			// Every line that reaches here while a cast is in flight is a
			// candidate; recording them is what makes a non-scoring debuff
			// diagnosable, because the landing text is the one thing no amount
			// of reading the code can tell you.
			if len(threatLandTried) < 8 {
				threatLandTried = append(threatLandTried, content)
			}
			if info, ok := threatSpellFor(threatPendCast); ok && info.Known &&
				at.Sub(threatPendCastAt) <= threatSpellWindow(info) {
				if re := threatLandMatcher(info.CastOnOther); re != nil {
					if lm := re.FindStringSubmatch(content); lm != nil {
						threatLandMatched = content
						norm := normThreatMob(lm[1])
						mt, ch := threatEnsureLocked(norm, lm[1], at, tun)
						changed = ch
						mt.threat += float64(info.Threat)
						mt.spellHate += info.Threat
						mt.lastAct = at
						threatPendCast = ""
						threatCastArmed.Store(false)
					}
				}
			}
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

// threatGearTripwireLocked disproves stale gear data: a plain swing whose verb
// matches neither equipped weapon means the inventory file predates a weapon
// swap. Sticky per gear fingerprint until the file (or character) changes.
// The generic "hit" verb is exempt — oddball held items log it. Caller must
// hold threatMu.
//
// Hit MAGNITUDE is deliberately not evidence any more. It bounded a hit by
// DMG × max_hit_mult, but that multiplier is the least trustworthy number in
// the model: a 60 rogue with two known 15-dmg weapons hit for 96 and 85, both
// exactly 5.667×DMG, against a table that then said 3.80. The wire fired on
// legitimate swings and switched off exact gear pricing in favour of inference
// that overstated his weapons by 49% — the wire's false positive cost far more
// accuracy than the stale gear it was watching for. A same-skill weapon swap
// is now caught only by a refreshed inventory file, which is the cheap fix.
func threatGearTripwireLocked(verb string, tun ThreatTuning) {
	class := strings.ToLower(strings.TrimSpace(classForCurrentChar()))
	eq := threatEquipmentLocked(tun, class)
	if !eq.ok {
		return
	}
	if verb == "hit" || verb == eq.mainVerb || (eq.dual && verb == eq.offVerb) {
		return
	}
	reason := fmt.Sprintf("swing skill %q matches neither equipped weapon", verb)
	threatEquipStale[eq.fp] = reason
	addStatus("DPS & Threat: gear data for %s looks stale (%s) — using inference. Run /outputfile inventory to refresh.", currentCharName, reason)
}

// ThreatZoneReset clears every hate ledger because the player zoned. Zoning
// removes you from the mob's hate list entirely — walk back in and resume the
// same fight and you start at zero — so carrying the old numbers across a zone
// load would overstate hate for the rest of the fight.
//
// Damage/DPS is deliberately NOT reset here (that lives in raiddps.go): damage
// already dealt stays dealt, it's only the hate that the zone wiped.
func ThreatZoneReset() {
	threatMu.Lock()
	had := len(threatMobs)
	threatMobs = map[string]*mobThreat{}
	threatCurrent, threatPendSpell = "", ""
	threatTools = ThreatToolsUI{}
	threatProcCreditAt = time.Time{}
	// Cadence timings are per-zone too — the swing clock restarts after a load.
	threatVerbCad = map[string]*threatCadence{}
	threatMu.Unlock()
	if had > 0 {
		addStatus("DPS & Threat: zoned — hate reset to zero on %d mob(s).", had)
		emitThreatChanged()
	}
}

// threatMobDiedLocked drops a dead mob's ledger; per-engagement tool counters
// reset with the current target's death.
func threatMobDiedLocked(norm string) bool {
	mt, ok := threatMobs[norm]
	if !ok {
		return false
	}
	threatLastFight = threatFightSnap{
		Mob:     mt.display,
		Dur:     mt.lastAct.Sub(mt.firstHit),
		Damage:  mt.damage,
		Threat:  int(mt.threat + 0.5),
		EndedAt: time.Now(),
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
	// Procs counted on this mob — the best of the three proc data sources
	// (see threatProcRateFor server-side); relaying it is what makes a tank's
	// proc hate real instead of assumed.
	Procs int `json:"procs"`
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
		Procs:    mt.procs,
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
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		// The response carries the zones hosting a live raid — feeds the
		// "assume haste-capped while raiding" rule.
		var out struct {
			RaidZones []string `json:"raid_zones"`
		}
		if json.NewDecoder(resp.Body).Decode(&out) == nil {
			storeThreatRaidZones(out.RaidZones)
		}
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
	TankName      string           `json:"tank_name"`
	TankProcsPM   float64          `json:"tank_procs_pm"`
	TankProcsSrc  string           `json:"tank_procs_src"`
	Own           *threatSnapWire  `json:"own"`
	Others        []threatSnapWire `json:"others"`
	Config        ThreatTuning     `json:"config"`
	ConfigVersion int64            `json:"config_version"`
	// RaidZones: the zones hosting a live raid, same as the POST response
	// carries — the table is fetched every 2s while engaged, which is what
	// keeps raid-vs-group mode fresh at the raid's first pull.
	RaidZones []string `json:"raid_zones"`
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
				// And the live-raid zones, which decide raid-vs-group mode.
				storeThreatRaidZones(t.RaidZones)
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
	Officer bool `json:"officer"`
	// RaidMode selects the overlay's layout: a live raid exists and the
	// viewer is standing in its zone. Raid mode shows the threat sections and
	// the raid's top 5; group mode is a pure DPS parser — no threat, six
	// rows, a group total. Independent of Engaged.
	RaidMode  bool    `json:"raid_mode"`
	Engaged   bool    `json:"engaged"`
	Mob       string  `json:"mob"`
	ElapsedS  int     `json:"elapsed_s"`
	DPS       float64 `json:"dps"`
	OwnThreat int     `json:"own_threat"`
	// OwnDamage is total damage dealt this fight, and SpellHate the part of
	// the threat that came from casting. A debuffer generates hate with zero
	// damage, so the overlay hides its DPS block rather than showing them a
	// permanent 0.0 — see PopoutThreat.svelte.
	OwnDamage int `json:"own_damage"`
	// OwnName lets the overlay pick the viewer out of the fight's top 5.
	OwnName   string `json:"own_name"`
	SpellHate int    `json:"spell_hate"`
	// Debug block: the live two-hand swing model — threat per swing, backed-
	// out weapon DMG, and estimated base delay per hand, plus the haste the
	// delay estimate divided out. Offhand fields are meaningful when Dual.
	SwingThreat  int           `json:"swing_threat"` // typical swing (hand average when dual)
	SwingSrc     string        `json:"swing_src"`    // "gear" or "inferred"
	Dual         bool          `json:"dual"`
	TPSMain      int           `json:"tps_main"`
	TPSOff       int           `json:"tps_off"`
	EstDmgMain   int           `json:"est_dmg_main"`
	EstDmgOff    int           `json:"est_dmg_off"`
	EstDelayMain float64       `json:"est_delay_main"` // EQ delay units; 0 = unknown
	EstDelayOff  float64       `json:"est_delay_off"`
	HastePct     int           `json:"haste_pct"`
	Tools        ThreatToolsUI `json:"tools"`
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
	// Who anchors the gauge when nobody is relaying tank threat, and how well
	// their procs are known ("bridge" counted / "macro" counted / "assumed").
	TankName     string  `json:"tank_name"`
	TankProcsPM  float64 `json:"tank_procs_pm"`
	TankProcsSrc string  `json:"tank_procs_src"`
}

// GetThreatDebug returns a copy-pasteable dump of everything the Threat
// Meter based its current numbers on — gear resolution, haste sources, the
// swing model, raw inference state, and the current/last fight. Admin
// calibration tool ("Copy Threat Debug" in Admin Settings).
func (a *App) GetThreatDebug() string {
	tun := threatTuningCached()
	class := strings.ToLower(strings.TrimSpace(classForCurrentChar()))
	level := levelForCurrentChar()
	inv := threatInventory()
	now := time.Now()

	var b strings.Builder
	fmt.Fprintf(&b, "=== DPS & Threat debug — %s ===\n", now.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "char=%s class=%q level=%d zone=%q linked=%v officer=%v\n",
		currentCharName, class, level, CurrentZone(), IsLinked(), isOfficerCached())

	fmt.Fprintf(&b, "\n[inventory file]\n")
	if !inv.found {
		b.WriteString("NOT FOUND — run /outputfile inventory in game (gear pricing and worn haste both need it)\n")
	} else {
		fmt.Fprintf(&b, "age=%s primary=%q secondary=%q worn_haste=%d%%\n",
			now.Sub(inv.modTime).Round(time.Minute), inv.primary, inv.secondary, inv.wornPct)
	}
	for _, name := range []string{inv.primary, inv.secondary} {
		if name == "" {
			continue
		}
		wi, cached := threatWeaponFor(name)
		fmt.Fprintf(&b, "item %q: cached=%v found=%v weapon=%v dmg=%d delay=%d skill=%q\n",
			name, cached, wi.Found, wi.Known, wi.Dmg, wi.Delay, wi.Skill)
	}

	threatMu.Lock()
	eq := threatEquipmentLocked(tun, class)
	staleWhy := ""
	if inv.found {
		staleWhy = threatEquipStale[currentCharName+"|"+inv.primary+"|"+inv.secondary]
	}
	sm := threatSwingModelLocked(tun)
	haste := threatTotalHastePctLocked(tun)
	worn := threatWornHaste(tun)
	spellPct, spellLeft := 0, time.Duration(0)
	if threatSpellHastePct > 0 && now.Before(threatSpellHasteEnd) {
		spellPct, spellLeft = threatSpellHastePct, threatSpellHasteEnd.Sub(now).Round(time.Second)
	}
	type verbDump struct {
		verb              string
		cur, prev, winMax int
		sets              int
		s1, s2a, s2b      float64
	}
	var verbs []verbDump
	for verb, hi := range threatVerbMax {
		vd := verbDump{verb: verb, cur: hi.cur, prev: hi.prev, winMax: hi.maxAt(now)}
		if c := threatVerbCad[verb]; c != nil {
			vd.sets = len(c.times)
			vd.s1 = threatStrideMedian(c.times, 0, 1, 4)
			vd.s2a = threatStrideMedian(c.times, 0, 2, 3)
			vd.s2b = threatStrideMedian(c.times, 1, 2, 3)
		}
		verbs = append(verbs, vd)
	}
	var curFight string
	if mt := threatMobs[threatCurrent]; mt != nil {
		curFight = fmt.Sprintf("mob=%q engaged=%ds damage=%d dps=%.1f threat=%d",
			mt.display, threatElapsedLocked(mt, now, tun), mt.damage, threatDPSLocked(mt, now, tun), int(mt.threat+0.5))
	}
	lastFight := threatLastFight
	tools := threatTools
	threatMu.Unlock()

	fmt.Fprintf(&b, "\n[gear pricing]\n")
	fmt.Fprintf(&b, "active=%v dual=%v stale=%q\n", eq.ok, eq.dual, staleWhy)
	capLevel := level
	if capLevel <= 0 {
		capLevel = 60
	}
	fmt.Fprintf(&b, "\n[haste]\napplied=%d%% worn=%d%% spell=%d%% (left %s) in_raid_zone=%v level_cap=%d raid_zones=%v\n",
		haste, worn, spellPct, spellLeft, threatInLiveRaidZone(), threatHasteCap(capLevel), threatRaidZoneList())
	fmt.Fprintf(&b, "\n[swing model]\nsrc=%s bonus=%d\nprimary: dmg=%.1f delay=%.1f tps=%.1f verb=%q\n",
		sm.Src, sm.Bonus, sm.DmgMain, sm.DelayMain, sm.TPSMain, sm.MainVerb)
	if sm.Dual {
		fmt.Fprintf(&b, "offhand: dmg=%.1f delay=%.1f tps=%.1f verb=%q\n", sm.DmgOff, sm.DelayOff, sm.TPSOff, sm.OffVerb)
	} else {
		b.WriteString("offhand: (single-hand model)\n")
	}
	fmt.Fprintf(&b, "\n[tuning]\nmax_hit_mult(%s)=%.2f swing_dmg_mult=%.1f miss_factor=%.2f proc_hate=%d specials=%v fists=%d/%d\n",
		class, threatMaxHitMult(class, tun), tun.SwingDmgMult, tun.MissFactor, tun.ProcHate, tun.SpecialHate, tun.FistsDmg, tun.FistsDelay)
	fmt.Fprintf(&b, "\n[inference raw]\n")
	fmt.Fprintf(&b, "crit announcements: seen=%d matched=%d last=%q (matched=%v)\n",
		threatCritSeen, threatCritHits, threatCritLast, threatCritLastHit)
	fmt.Fprintf(&b, "proc lines: seen=%d self=%d last=%q (self=%v) | broadcast form seen=%d last=%q\n",
		threatProcSeen, threatProcSelf, threatProcLast, threatProcSelfOK, threatProc3p, threatProc3pLast)
	fmt.Fprintf(&b, "proc effect lines (trigger list): seen=%d credited=%d last=%q\n",
		threatProcFx, threatProcFxOK, threatProcFxLast)
	if threatProcFxSkip != "" {
		fmt.Fprintf(&b, "  last effect line NOT credited: %s\n", threatProcFxSkip)
	}
	fmt.Fprintf(&b, "assumed proc rate (no bridge/macro): %.2f/min at dex %d\n",
		threatAssumedProcsPerMin(tun, threatClassDual(class)), tun.AssumedDex)
	fmt.Fprintf(&b, "assumed evade shed for unseen rogues: %.2f/min (%.0f hate/min)\n",
		tun.AssumedEvadePerMin, tun.AssumedEvadePerMin*float64(tun.Reducers["evade"]))
	if len(spellUnrated) > 0 {
		var names []string
		for n, c := range spellUnrated {
			names = append(names, fmt.Sprintf("%s ×%d", n, c))
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "spells cast with NO threat rating (set eq_spells.threat): %s\n",
			strings.Join(names, ", "))
	}

	// Last cast, step by step. A debuff that does no damage scores only if all
	// five of these line up, and when it doesn't there is otherwise nothing to
	// look at — the failure is silent by construction.
	b.WriteString("\n[last spell cast]\n")
	if threatCastLast == "" {
		b.WriteString("(no non-reducer cast seen this session)\n")
	} else {
		fmt.Fprintf(&b, "1. cast line parsed:  %q (%s ago)\n",
			threatCastLast, now.Sub(threatCastLastAt).Round(time.Second))
		info, ok := threatSpellFor(threatCastLast)
		fmt.Fprintf(&b, "2. found in eq_spells: %v\n", ok && info.Found)
		fmt.Fprintf(&b, "3. threat curated:     %v (threat=%d)  <- eq_spells.threat, must be > 0\n",
			info.Known, info.Threat)
		fmt.Fprintf(&b, "4. cast_on_other:      %q\n", info.CastOnOther)
		switch {
		case strings.TrimSpace(info.CastOnOther) == "":
			b.WriteString("   -> EMPTY: a non-damaging debuff can never be seen to land. Set it.\n")
		case threatLandMatcher(info.CastOnOther) == nil:
			b.WriteString("   -> no \"Someone\"/\"Soandso\" placeholder, so no target can be" +
				" captured. Fix it.\n")
		default:
			fmt.Fprintf(&b, "   -> matcher: %s\n", threatLandMatcher(info.CastOnOther))
		}
		fmt.Fprintf(&b, "   window: cast_time %q + 8s slack = %s\n",
			info.CastTime, threatSpellWindow(info).Round(time.Second))
		if threatLandMatched != "" {
			fmt.Fprintf(&b, "5. MATCHED: %q\n", threatLandMatched)
		} else if len(threatLandTried) == 0 {
			b.WriteString("5. no lines arrived while the cast was in flight\n")
		} else {
			b.WriteString("5. NO MATCH. Lines offered while the cast was in flight —\n" +
				"   compare one of these against cast_on_other above:\n")
			for _, l := range threatLandTried {
				fmt.Fprintf(&b, "     %q\n", l)
			}
		}
	}
	if len(verbs) == 0 {
		b.WriteString("(no plain swings recorded yet)\n")
	}
	for _, vd := range verbs {
		fmt.Fprintf(&b, "verb=%q window_max=%d (cur=%d prev=%d) sets=%d stride1=%.1f stride2=%.1f/%.1f\n",
			vd.verb, vd.winMax, vd.cur, vd.prev, vd.sets, vd.s1, vd.s2a, vd.s2b)
	}
	fmt.Fprintf(&b, "\n[fights]\ncurrent: %s\n", orNone(curFight))
	if lastFight.Mob != "" {
		fmt.Fprintf(&b, "last: mob=%q dur=%s damage=%d threat=%d ended=%s ago\n",
			lastFight.Mob, lastFight.Dur.Round(time.Second), lastFight.Damage, lastFight.Threat,
			now.Sub(lastFight.EndedAt).Round(time.Second))
	} else {
		b.WriteString("last: (none)\n")
	}
	fmt.Fprintf(&b, "tools: conc %d/%d jolt %d/%d evade %d/%d\n",
		tools.ConcOK, tools.ConcFail, tools.JoltOK, tools.JoltFail, tools.EvadeOK, tools.EvadeFail)
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// threatRaidZoneList snapshots the live-raid zone set for the debug dump.
func threatRaidZoneList() []string {
	threatRaidZoneMu.Lock()
	defer threatRaidZoneMu.Unlock()
	out := make([]string, 0, len(threatRaidZones))
	for z := range threatRaidZones {
		out = append(out, z)
	}
	sort.Strings(out)
	return out
}

// GetThreatMeter returns the overlay's full state. Open to everyone: the
// parse describes the viewer's own fight, which is the least officer-ish
// thing in the app. Unlinked clients run it local-only — group mode, own log,
// no server round-trips — because a parser that needs an account to show
// your own swings would be refusing its own point.
func (a *App) GetThreatMeter() ThreatMeterUI {
	out := ThreatMeterUI{Others: []ThreatRowUI{}}
	out.Zones = threatTuningCached().Gauge
	out.Officer = true
	out.OwnName = currentCharName
	// Raid mode: a live raid exists AND the viewer stands in its zone. A
	// member XPing in Chardok during a ToV raid keeps the group parser; the
	// same member zoning into ToV flips to the raid layout within a poll.
	out.RaidMode = IsLinked() && threatInLiveRaidZone()

	tun := threatTuningCached()
	now := time.Now()
	threatMu.Lock()
	pruneThreatMobsLocked(now, tun)
	mt := threatMobs[threatCurrent]
	if mt != nil && now.Sub(mt.lastAct) <= threatEngagedIdle(tun) {
		out.Engaged = true
		out.Mob = mt.display
		out.ElapsedS = threatElapsedLocked(mt, now, tun)
		out.DPS = threatDPSLocked(mt, now, tun)
		out.OwnThreat = int(mt.threat + 0.5)
		out.OwnDamage = mt.damage
		out.SpellHate = mt.spellHate
	} else if !threatEngageLatch.IsZero() && now.Sub(threatEngageLatch) <= threatEngagedIdle(tun) {
		// Combat proven by lines that named no mob (a resisted opener, an
		// incoming nuke): the overlay opens with an unnamed header rather
		// than staying dark through a fight that is plainly happening.
		out.Engaged = true
	}
	sm := threatSwingModelLocked(tun)
	out.Dual = sm.Dual
	out.SwingSrc = sm.Src
	out.TPSMain = int(sm.TPSMain + 0.5)
	out.EstDmgMain = int(sm.DmgMain + 0.5)
	out.EstDelayMain = float64(int(sm.DelayMain*10+0.5)) / 10
	out.HastePct = sm.HastePct
	if sm.Dual {
		out.TPSOff = int(sm.TPSOff + 0.5)
		out.EstDmgOff = int(sm.DmgOff + 0.5)
		out.EstDelayOff = float64(int(sm.DelayOff*10+0.5)) / 10
		out.SwingThreat = int((sm.TPSMain+sm.TPSOff)/2 + 0.5)
	} else {
		out.SwingThreat = int(sm.TPSMain + 0.5)
	}
	out.Tools = threatTools
	threatMu.Unlock()
	if !out.Engaged {
		return out
	}
	// The server table exists for the threat comparison, which only the raid
	// layout draws. Group mode (and the unnamed latch, which has no mob to
	// ask about) skips the round-trip — raid-zone freshness still rides the
	// threat POSTs, so a raid starting around a group fight flips the mode.
	if !out.RaidMode || !IsLinked() || out.Mob == "" {
		return out
	}

	tbl := fetchThreatTable(out.Mob)
	if tbl == nil {
		return out
	}
	out.RaidActive = tbl.RaidActive
	out.Zones = tbl.Config.Gauge
	out.TankSource = tbl.TankSource
	out.TankName = tbl.TankName
	out.TankProcsPM = tbl.TankProcsPM
	out.TankProcsSrc = tbl.TankProcsSrc

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

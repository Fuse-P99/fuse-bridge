package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Raid DPS rollup: unlike the DPS & Threat overlay (first-person only), this parses
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
	//
	// "were" is here for the line's other direction — "You were hit by non-melee
	// for 60 points of damage." is a MOB's shield burning us. Without it that
	// line falls through to the melee pattern below, which happily reads it as
	// "You were" hitting something called "by non-melee" and opens a board with
	// that name. Matching it here hands it to the self-target gate instead.
	rdpsNonMeleeRE = regexp.MustCompile(`^(.+?) (?:was|were) hit by non-melee for (\d+) points? of damage\.$`)
	// Name-form non-melee ("Carboload hit X for N points of non-melee damage.")
	// — the old client's rendering of OUR own proc/nuke damage.
	rdpsSelfNonMeleeRE = regexp.MustCompile("^([\\w`' -]+?) hit (.+?) for (\\d+) points? of non-melee damage\\.$")
)

// rdpsSpan is when an attacker was actually swinging at this mob: first damage
// seen to last. A raid's DPS is measured two ways and they answer different
// questions — damage over the whole fight (what the meter shows) tells you what
// the raid did; damage over the seconds someone was ENGAGED tells you how hard
// they hit while they were on it. A rogue who arrives at 60% is not a bad rogue.
type rdpsSpan struct {
	first time.Time
	last  time.Time
}

// rdpsFight is one mob's damage board as this client saw it.
type rdpsFight struct {
	display  string
	first    time.Time
	last     time.Time
	melee    map[string]int       // attacker (as logged) → damage seen
	span     map[string]*rdpsSpan // attacker → when they were on this mob
	selfNonM int                  // OUR non-melee only: damage shield + procs
	// confirmed: WE have damaged this thing, so it is certainly a mob.
	//
	// Nothing in a damage line distinguishes a mob from a player — "Vyemm hits
	// Carboload" is shaped exactly like a player hitting a mob named Carboload,
	// which is how a boss beating on the tank opens a board named after the
	// tank. In a raid the server settles it from the called target and the
	// listed adds. Outside one there is no such list, and this is what's left
	// that we can be sure of: you don't swing at your own group.
	confirmed bool
}

// noteSpan widens an attacker's engagement to include a damage line at `at`.
// The mob's death ends it on its own: the slain line drops this client's board,
// so nothing after the kill can extend anyone's span.
func (f *rdpsFight) noteSpan(who string, at time.Time) {
	s := f.span[who]
	if s == nil {
		f.span[who] = &rdpsSpan{first: at, last: at}
		return
	}
	if at.Before(s.first) {
		s.first = at
	}
	if at.After(s.last) {
		s.last = at
	}
}

var (
	rdpsMu     sync.Mutex
	rdpsFights = map[string]*rdpsFight{} // normThreatMob → board
	rdpsSendAt time.Time
)

const (
	rdpsIdleReset = 5 * time.Minute
	// rdpsFightGap: no damage on a mob for this long ends the fight, and the
	// next damage starts a fresh board. A raid boss that nobody has touched for
	// three minutes is not still being fought — the pull failed, and the
	// re-pull deserves its own parse. Kept well above any lull inside a real
	// fight, and well below the time a wipe takes to recover from.
	//
	// The server applies the same rule to the same boundary (raidDPS.go), which
	// is what makes the two agree: both sides forget the failed attempt, so
	// max-merge has nothing stale left to prefer.
	rdpsFightGap  = 3 * time.Minute
	rdpsPostEvery = 10 * time.Second
	// A line must be this fresh in WALL CLOCK terms to count. Startup replay
	// stamps entries with old log times; posting those would hand the server a
	// number max can never walk back.
	rdpsFreshWindow = 30 * time.Second
	// How many boards one post cycle will send. A raid zone can have several
	// fights going; the busiest few cover the one the raid is actually on, and
	// the server drops everything that isn't the called target or a listed add
	// anyway.
	rdpsMaxPost = 6
)

// rdpsSkipTarget reports whether a damage line's target can't be a mob we are
// fighting, because it's us. Damage TAKEN must never open a board: EQ writes
// "The Avatar of War hits YOU for 1000 points of damage.", which is the same
// shape as a player hitting a mob, so without this the mob beating on us opens
// a board named after US — with the mob itself sitting on it as an attacker.
func rdpsSkipTarget(name string) bool {
	n := strings.TrimSpace(name)
	if strings.EqualFold(n, "you") {
		return true
	}
	return currentCharName != "" && strings.EqualFold(n, currentCharName)
}

// rdpsAttacker normalizes an attacker name, returning "" only for names that
// cannot own a damage row: the empty string, and the board's own mob.
//
// Multi-word names are ALLOWED, and that is deliberate. Charmed pets are a
// large share of raid damage — a charmed dire wolf routinely out-parses most of
// the raid — and they carry mob names ("a Drakkel Dire Wolf"), so the old
// "players are one word" rule dropped essentially every one of them.
//
// What keeps hostile NPCs out is not the name, it's the BOARD. Boards are keyed
// by the damage target, so a mob beating on a raider opens a board named after
// that RAIDER, and the server only aggregates boards that are the called target
// or a listed add (rdpsEligible) — those never reach a meter or a parse. On a
// board that IS the boss, everything hitting it is on our side by definition.
//
// Threat is unaffected: that parser is first-person only (threat.go reads "You
// hit ..." and nothing else), so a pet has no way to register hate against a
// mob no matter what this returns.
//
// Two pets sharing a name ("a Drakkel Dire Wolf" charmed by two enchanters)
// land on one row. That is consistent rather than lossy: every client also
// can't tell them apart, so each reports the pair's combined damage, and
// max-merge across clients still picks the best single observation of the
// same quantity.
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
			(m[1] == "You" || strings.EqualFold(m[1], currentCharName)) && !rdpsSkipTarget(m[2]) {
			dmg, _ := strconv.Atoi(m[3])
			f := rdpsEnsureLocked(m[2], at)
			f.selfNonM += dmg
			f.last = at
			f.confirmed = true
			if currentCharName != "" {
				f.noteSpan(currentCharName, at)
			}
		}
		return
	}
	if m := rdpsNonMeleeRE.FindStringSubmatch(content); m != nil {
		// Anonymous — only ever OUR damage shield or proc, since a character
		// sees no one else's. Credited to us, never to a named row. The same
		// line describes a MOB's shield burning us ("You were hit by non-melee
		// for 60 points of damage."), which is damage taken, not dealt.
		if rdpsSkipTarget(m[1]) {
			return
		}
		dmg, _ := strconv.Atoi(m[2])
		f := rdpsEnsureLocked(m[1], at)
		f.selfNonM += dmg
		f.last = at
		// Our own shield or proc went off on it, which only happens to
		// something we are fighting.
		f.confirmed = true
		if currentCharName != "" {
			f.noteSpan(currentCharName, at)
		}
		return
	}
	if m := rdpsHitRE.FindStringSubmatch(content); m != nil {
		if rdpsSkipTarget(m[2]) {
			return
		}
		f := rdpsEnsureLocked(m[2], at)
		who := rdpsAttacker(m[1], f.display)
		if who == "" {
			return
		}
		dmg, _ := strconv.Atoi(m[3])
		f.melee[who] += dmg
		f.last = at
		f.noteSpan(who, at)
		if currentCharName != "" && strings.EqualFold(who, currentCharName) {
			f.confirmed = true
		}
	}
}

func rdpsEnsureLocked(mob string, at time.Time) *rdpsFight {
	norm := normThreatMob(mob)
	f := rdpsFights[norm]
	if f == nil {
		f = &rdpsFight{display: strings.TrimSpace(mob), first: at,
			melee: map[string]int{}, span: map[string]*rdpsSpan{}}
		rdpsFights[norm] = f
		return f
	}
	// Damage after a long silence is a NEW fight on the same mob — the raid
	// wiped, or gave up and came back. Without this the second attempt piles
	// onto the first: totals add up across both, and the fight clock spans the
	// corpse recovery in between, which drags everyone's DPS toward zero.
	// A kill has its own reset; this is the one for the attempts that fail.
	if at.Sub(f.last) > rdpsFightGap {
		f.first, f.last = at, at
		f.melee = map[string]int{}
		f.span = map[string]*rdpsSpan{}
		f.selfNonM = 0
		// display and confirmed survive: it's the same mob, and we already
		// know it's a mob.
	}
	return f
}

func rdpsPruneLocked(now time.Time) {
	for k, f := range rdpsFights {
		if now.Sub(f.last) > rdpsIdleReset {
			delete(rdpsFights, k)
		}
	}
}

// RaidDPSResetMob drops a dead mob's board — the fight is over, and the next
// spawn of the same name starts clean.
func RaidDPSResetMob(mob string) {
	norm := normThreatMob(mob)
	rdpsMu.Lock()
	delete(rdpsFights, norm)
	rdpsMu.Unlock()
}

// rdpsRowWire is one attacker's damage as this client saw it.
type rdpsRowWire struct {
	Name  string `json:"name"`
	Melee int    `json:"melee"`
	// EngagedS is how long this attacker was on the mob: last damage minus
	// first. A DURATION, deliberately, not two timestamps — the server merges
	// reports from a dozen machines whose clocks agree only roughly, and a
	// duration measured inside one log is immune to that. The server keeps the
	// longest span reported, on the same reasoning as the damage itself: no
	// client can see more than happened, so the widest witness is the truest.
	EngagedS int `json:"engaged_s"`
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
	// SelfEngagedS is our own span on this mob, for the case where non-melee
	// is all we contributed.
	SelfEngagedS int `json:"self_engaged_s"`
	// Confirmed: we damaged this ourselves, so it is certainly a mob and not a
	// board named after a groupmate something was hitting. Outside a raid this
	// is the only thing that makes a board servable.
	Confirmed bool `json:"confirmed"`
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
	// SDPS over the whole fight; EDPS over the seconds engaged.
	SDPS     float64 `json:"sdps"`
	EDPS     float64 `json:"edps"`
	EngagedS int     `json:"engaged_s"`
	Pct      float64 `json:"pct"`
	Dead     bool    `json:"dead"`
	// Reporters/Spread expose how well-witnessed a number is: how many clients
	// saw this attacker, and how far apart their totals were.
	Reporters int `json:"reporters"`
	Spread    int `json:"spread"`
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
	// Final: the fight is over and these numbers are settled.
	Final bool `json:"final"`
}

type rdpsCacheEntry struct {
	data RaidDPSUI
	at   time.Time
}

var (
	rdpsTblMu sync.Mutex
	rdpsTbl   = map[string]rdpsCacheEntry{} // mob ("" = whatever's live) → board
)

const (
	rdpsTblTTL = 3 * time.Second
	// A finished fight's numbers never change again, so a page of completed
	// raid cards costs one request each per minute instead of per poll tick.
	rdpsFinalTTL = 60 * time.Second
)

// GetRaidDPS returns a raid damage board. mob scopes it to one fight — a raid
// card passes its own target, so a completed card keeps showing ITS damage
// instead of whatever is being fought now; "" asks for the live fight, which is
// what the overlay wants. Officer-only, like the threat gauge, and cached so
// several cards polling at once cost one round-trip.
func (a *App) GetRaidDPS(mob string) RaidDPSUI {
	if !IsLinked() {
		return RaidDPSUI{Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}}
	}
	key := strings.ToLower(strings.TrimSpace(mob))
	rdpsTblMu.Lock()
	if e, ok := rdpsTbl[key]; ok {
		ttl := rdpsTblTTL
		if e.data.Final {
			ttl = rdpsFinalTTL
		}
		if time.Since(e.at) < ttl {
			rdpsTblMu.Unlock()
			return e.data
		}
	}
	rdpsTblMu.Unlock()

	out := fetchRaidDPS(mob, false, false)
	rdpsTblMu.Lock()
	rdpsTbl[key] = rdpsCacheEntry{data: out, at: time.Now()}
	rdpsTblMu.Unlock()
	return out
}

// GetRaidParse returns the FULL damage table for a fight — every attacker, not
// the meter's top five. Opened on demand from the parse button, so it skips the
// meter's cache and asks the server directly: a parse window that opened on a
// three-second-old snapshot of a live fight would be reporting the past.
func (a *App) GetRaidParse(mob string) RaidDPSUI {
	if !IsLinked() {
		return RaidDPSUI{Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}}
	}
	return fetchRaidDPS(mob, true, false)
}

// GetFightDPS is GetRaidDPS for the personal DPS/threat overlay: same board,
// same ranking, but it also answers outside a raid, where it falls back to
// whatever we're actually hitting. The Raid DPS board deliberately does not —
// it is about the raid, and showing a group pull there would be a lie about
// what's happening.
func (a *App) GetFightDPS() RaidDPSUI {
	if !IsLinked() {
		return RaidDPSUI{Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}}
	}
	rdpsTblMu.Lock()
	if e, ok := rdpsTbl[rdpsAnyKey]; ok {
		ttl := rdpsTblTTL
		if e.data.Final {
			ttl = rdpsFinalTTL
		}
		if time.Since(e.at) < ttl {
			rdpsTblMu.Unlock()
			return e.data
		}
	}
	rdpsTblMu.Unlock()

	out := fetchRaidDPS("", false, true)
	rdpsTblMu.Lock()
	rdpsTbl[rdpsAnyKey] = rdpsCacheEntry{data: out, at: time.Now()}
	rdpsTblMu.Unlock()
	return out
}

// rdpsAnyKey caches the any-fight board separately from the raid board — they
// can legitimately differ, and one must never be served for the other.
const rdpsAnyKey = "\x00any"

func fetchRaidDPS(mob string, full, any bool) RaidDPSUI {
	out := RaidDPSUI{Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}}
	base := strings.TrimSuffix(serverURL, "/submit")
	q := url.Values{}
	if strings.TrimSpace(mob) != "" {
		q.Set("mob", mob)
	}
	if full {
		q.Set("full", "1")
	}
	if any {
		q.Set("any", "1")
	}
	u := base + "/raiddps"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
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
	return out
}

// MaybeSendRaidDPS posts this client's view of every fight it can currently
// see, throttled. Fire-and-forget, like the threat snapshot.
//
// Every live board goes up, not just the most recently hit one. Which mob is
// "current" flips line by line in a raid — a boss swing, an add, a mob hitting
// somebody — so picking one board here meant posting a different fight every
// cycle, at random, and the board on screen jumped with it. The server holds
// the raid's called target and its listed adds, so it is the only side that can
// say which of these fights the raid is on; this side just reports what it saw.
func (s *Sender) MaybeSendRaidDPS() {
	if !IsLinked() || currentCharName == "" {
		return
	}
	now := time.Now()
	rdpsMu.Lock()
	if now.Sub(rdpsSendAt) < rdpsPostEvery {
		rdpsMu.Unlock()
		return
	}
	rdpsSendAt = now
	type pending struct {
		last time.Time
		body rdpsPostWire
	}
	var out []pending
	for _, f := range rdpsFights {
		if now.Sub(f.last) > rdpsFreshWindow {
			continue
		}
		p := rdpsPostWire{
			Toon:         currentCharName,
			Mob:          f.display,
			EngagedS:     int(f.last.Sub(f.first).Seconds()),
			SelfNonMelee: f.selfNonM,
			Confirmed:    f.confirmed,
			Rows:         make([]rdpsRowWire, 0, len(f.melee)),
		}
		for name, dmg := range f.melee {
			eng := 0
			if s := f.span[name]; s != nil {
				eng = int(s.last.Sub(s.first).Seconds())
			}
			p.Rows = append(p.Rows, rdpsRowWire{Name: name, Melee: dmg, EngagedS: eng})
		}
		// Our own shield/proc damage rides on SelfNonMelee, outside Rows, so a
		// character whose only contribution to this mob was non-melee would
		// otherwise report no span at all.
		if p.SelfNonMelee > 0 {
			if s := f.span[currentCharName]; s != nil {
				p.SelfEngagedS = int(s.last.Sub(s.first).Seconds())
			}
		}
		if len(p.Rows) == 0 && p.SelfNonMelee == 0 {
			continue
		}
		out = append(out, pending{last: f.last, body: p})
	}
	rdpsMu.Unlock()
	if len(out) == 0 {
		return
	}
	// Busiest first, so the cap sheds the quiet edges of the zone rather than
	// the fight.
	sort.Slice(out, func(i, j int) bool { return out[i].last.After(out[j].last) })
	if len(out) > rdpsMaxPost {
		out = out[:rdpsMaxPost]
	}

	for _, p := range out {
		body, err := json.Marshal(p.body)
		if err != nil {
			continue
		}
		go func(body []byte) {
			base := strings.TrimSuffix(s.serverURL, "/submit")
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
		}(body)
	}
}

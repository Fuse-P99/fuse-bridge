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
	// The verb is CAPTURED (group 2): "backstabs" is the only class tell a
	// damage line carries, and it settles a rogue whose class the roster and
	// /who both missed.
	rdpsHitRE = regexp.MustCompile(`^(.+?) (` + threatVerbs + `|hits|kicks|slashes|crushes|pierces|bashes|slams|strikes|punches|backstabs|bites|claws|smashes|slices|gores|mauls|rends|burns|stings|sweeps) (.+?) for (\d+) points? of damage\.$`)
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
	// Pet responses. EQ writes NPC and pet speech WITHOUT the comma player
	// speech carries — "Korakaz says 'Following you, Master.'" against "Bob
	// says, 'hi'" — so " says '" only ever follows a non-player. Every pet
	// response then addresses its owner, which is the second half of the test.
	//
	// This is the only way to identify a pet whose name is shaped exactly like
	// a player's, and that case is not hypothetical: in a real AoW log a
	// charmed giant named Korakaz parsed as the raid's third-best DPSer.
	rdpsPetSayRE = regexp.MustCompile("^([A-Za-z`' -]+) says '(.+)'$")
	// The same response delivered as a TELL, which EQ sends only to the pet's
	// owner. That makes this line proof of ownership: whoever's log it appears
	// in owns the pet, so the row can be labelled with a person's name instead
	// of "a sepulcher skeleton".
	rdpsPetTellRE = regexp.MustCompile("^([A-Za-z`' -]+) tells you, '(.+)'$")
)

// rdpsPetPhrase reports whether a line of NPC speech is a pet answering its
// owner. Pets address you as Master or as one of two flowery variants; no other
// NPC dialogue in the game is built that way.
func rdpsPetPhrase(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "master") ||
		strings.Contains(l, "oh splendid one") ||
		strings.Contains(l, "oh great one")
}

// Pets identified by their own speech, remembered for the session. A pet
// announces itself when it is given an order, which may be minutes before or
// after the damage that needs classifying — so this outlives any one board.
type rdpsPetInfo struct {
	at    time.Time
	owner string // "" until a tell proves it; never overwritten once set
}

var (
	rdpsPetMu sync.Mutex
	rdpsPets  = map[string]rdpsPetInfo{} // lower(name) → what we know
)

const rdpsPetTTL = 8 * time.Hour

// notePet records a pet. owner is set only from the tell form; the say form
// passes "" because it proves the thing is a pet but not whose.
func notePet(name, owner string) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return
	}
	rdpsPetMu.Lock()
	info := rdpsPets[n]
	info.at = time.Now()
	if owner != "" {
		info.owner = owner
	}
	rdpsPets[n] = info
	rdpsPetMu.Unlock()
}

// petInfo reports whether this name has identified itself as a pet, and whose
// it is if a tell has proved it. Charm breaks and re-charms keep the name
// valid, so the TTL is long — a name that has ever answered "Master" is not
// going to turn out to be a player.
func petInfo(name string) (bool, string) {
	n := strings.ToLower(strings.TrimSpace(name))
	rdpsPetMu.Lock()
	info, ok := rdpsPets[n]
	rdpsPetMu.Unlock()
	if !ok || time.Since(info.at) >= rdpsPetTTL {
		return false, ""
	}
	return true, info.owner
}

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
	backstab map[string]bool      // attacker → has backstabbed, i.e. is a rogue
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

	// Pet self-identification arrives as speech, not damage, so it has to be
	// tested ahead of the damage gate. Cheap suffix first: every pet response
	// ends with a period inside the closing quote, which no damage line does.
	//
	// The tell form is checked first because it carries strictly more: EQ sends
	// it only to the owner, so seeing it in THIS log names the owner.
	if strings.HasSuffix(content, ".'") && GetSettings().PetInfo {
		if m := rdpsPetTellRE.FindStringSubmatch(content); m != nil && rdpsPetPhrase(m[2]) {
			notePet(m[1], currentCharName)
			return
		}
		if m := rdpsPetSayRE.FindStringSubmatch(content); m != nil && rdpsPetPhrase(m[2]) {
			notePet(m[1], "")
			return
		}
	}
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
		// Groups: 1 attacker, 2 verb, 3 target, 4 damage.
		if rdpsSkipTarget(m[3]) {
			return
		}
		f := rdpsEnsureLocked(m[3], at)
		who := rdpsAttacker(m[1], f.display)
		if who == "" {
			return
		}
		dmg, _ := strconv.Atoi(m[4])
		f.melee[who] += dmg
		f.last = at
		f.noteSpan(who, at)
		// Only rogues backstab. Recorded per board and shipped with the row so
		// the server can class someone the roster has never seen.
		if m[2] == "backstabs" || m[2] == "backstab" {
			f.backstab[who] = true
		}
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
			melee: map[string]int{}, span: map[string]*rdpsSpan{},
			backstab: map[string]bool{}}
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
	// Class tells this client can prove, which the server uses only when the
	// roster and /who have both failed to class the name. Both are sticky on
	// the server: one witness is enough, and no later report can unsay it.
	Backstab bool `json:"backstab,omitempty"`
	Pet      bool `json:"pet,omitempty"`
	// Owner is the character this pet belongs to, known only to the client that
	// received its tell. The server relabels the row "<Owner> + Pet" so a big
	// number reads as a person's pet rather than as a mob nobody recognises.
	Owner string `json:"owner,omitempty"`
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
	// Zone is where this client is standing — damage lines are only visible in
	// the zone they happen in, so it names the fight's zone too. The server
	// uses it to keep the raid board locked to the raid's own zone: without
	// it, a linked client XPing elsewhere feeds the "what the raid is killing"
	// fallback a confirmed fight from the wrong side of the world.
	Zone string `json:"zone,omitempty"`
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
	Officer bool `json:"officer"`
	// Mode says which layout the personal overlay should draw for this data:
	// "raid" (the server board, verbatim) or "group" (local + collated).
	// Empty from the raid-card fetchers, which have exactly one meaning.
	Mode     string  `json:"mode"`
	Mob      string  `json:"mob"`
	EngagedS int     `json:"engaged_s"`
	Total    int     `json:"total"`
	RaidDPS  float64 `json:"raid_dps"`
	// MobDPS is the fight target's own outgoing melee rate over the same
	// window, already a whole number; 0 means unknown and hides the line.
	MobDPS  int               `json:"mob_dps"`
	Top     []RaidDPSPlayerUI `json:"top"`
	Classes []RaidDPSClassUI  `json:"classes"`
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

// GetFightDPS is the DPS board for the personal DPS & Threat overlay, and it
// is mode-aware:
//
//   - RAID mode (live raid + viewer standing in its zone): the server's live
//     board, verbatim — same endpoint and ranking as the raid card's meter,
//     so the two agree row for row (or sit one server update apart). Fetched
//     FULL so the overlay can pin the viewer's own row under the top five.
//   - GROUP mode (everything else, including unlinked): a local parse of the
//     fight in front of the viewer, collated with the server's board for
//     that same mob when linked — that board is where another client's spell
//     damage lives, since no other log can see it. Unlinked clients get the
//     local parse alone.
func (a *App) GetFightDPS() RaidDPSUI {
	if IsLinked() && threatInLiveRaidZone() {
		return fightDPSRaid()
	}
	return fightDPSGroup()
}

// Cache slots for the overlay's two modes. Distinct from each other and from
// the raid card's per-mob entries — each answers a different question, and
// one must never be served for another.
const (
	rdpsRaidFullKey = "\x00raidfull"
	rdpsGrpKeyPre   = "\x00grp:"
)

func rdpsCached(key string) (RaidDPSUI, bool) {
	rdpsTblMu.Lock()
	defer rdpsTblMu.Unlock()
	e, ok := rdpsTbl[key]
	if !ok {
		return RaidDPSUI{}, false
	}
	ttl := rdpsTblTTL
	if e.data.Final {
		ttl = rdpsFinalTTL
	}
	if time.Since(e.at) >= ttl {
		return RaidDPSUI{}, false
	}
	return e.data, true
}

func rdpsStore(key string, d RaidDPSUI) {
	rdpsTblMu.Lock()
	rdpsTbl[key] = rdpsCacheEntry{data: d, at: time.Now()}
	rdpsTblMu.Unlock()
}

func fightDPSRaid() RaidDPSUI {
	if d, ok := rdpsCached(rdpsRaidFullKey); ok {
		return d
	}
	out := fetchRaidDPS("", true, false)
	out.Mode = "raid"
	rdpsStore(rdpsRaidFullKey, out)
	return out
}

// fightDPSGroup builds the group-mode board. The server is the authority on
// everyone ELSE — it holds the other clients' spell damage and their pet
// folds — while the local parse is the authority on the viewer's own last few
// seconds, which the 10s post cycle hasn't delivered yet. So: server rows
// wholesale when the board exists, the viewer's own row max-overlaid with the
// local number, and the pure local parse when there's no server board at all
// (unlinked, or the first post cycle of a fight).
func fightDPSGroup() RaidDPSUI {
	out := RaidDPSUI{Mode: "group", Top: []RaidDPSPlayerUI{}, Classes: []RaidDPSClassUI{}}
	// Lock order: threatMu (inside these two) strictly before rdpsMu.
	norm, display, engaged := threatCurrentFight()
	idle := threatEngagedIdle(threatTuningCached())
	me := currentCharName
	now := time.Now()

	type loc struct {
		display      string
		melee, other int
		hasPet       bool
	}
	local := map[string]*loc{} // lower(final label) → local numbers
	secs := 0.0

	rdpsMu.Lock()
	f := rdpsFights[norm]
	if !engaged || f == nil {
		// The first-person ledger has nothing — a viewer watching their group
		// fight without swinging. The most recently active local board is the
		// fight in front of them.
		f = nil
		for _, cand := range rdpsFights {
			if now.Sub(cand.last) > idle {
				continue
			}
			if f == nil || cand.last.After(f.last) {
				f = cand
			}
		}
	}
	if f != nil {
		display = f.display
		secs = f.last.Sub(f.first).Seconds()
		row := func(label string) *loc {
			k := strings.ToLower(label)
			l := local[k]
			if l == nil {
				l = &loc{display: label}
				local[k] = l
			}
			return l
		}
		for name, dmg := range f.melee {
			// Mirror the server's owned-pet fold so the two boards agree on
			// row identity: an owned pet's damage lands on the owner's line.
			label := name
			pet := false
			if _, owner := petInfo(name); owner != "" && !strings.EqualFold(owner, name) {
				label, pet = owner, true
			}
			l := row(label)
			l.melee += dmg
			if pet {
				l.hasPet = true
			}
		}
		if f.selfNonM > 0 && me != "" {
			row(me).other += f.selfNonM
		}
		// Apply the "+ Pet" suffix the way the server does, re-keying so the
		// label matches the server's row name for the same pair.
		for k, l := range local {
			if l.hasPet {
				delete(local, k)
				l.display += " + Pet"
				local[strings.ToLower(l.display)] = l
			}
		}
	}
	rdpsMu.Unlock()

	rows := map[string]*RaidDPSPlayerUI{}
	if IsLinked() && display != "" {
		key := rdpsGrpKeyPre + strings.ToLower(display)
		srv, ok := rdpsCached(key)
		if !ok {
			srv = fetchRaidDPS(display, true, false)
			rdpsStore(key, srv)
		}
		for i := range srv.Top {
			r := srv.Top[i]
			rows[strings.ToLower(r.Name)] = &r
		}
		if float64(srv.EngagedS) > secs {
			secs = float64(srv.EngagedS)
		}
	}
	if len(rows) == 0 {
		for _, l := range local {
			rows[strings.ToLower(l.display)] = &RaidDPSPlayerUI{
				Name: l.display, Melee: l.melee, Other: l.other,
			}
		}
	} else if me != "" {
		// Own-row freshness: whichever label the viewer wears ("Me" or
		// "Me + Pet"), the local number may be ahead of the server's by up to
		// a post cycle — max keeps the better observation, same rule the
		// server itself merges by.
		for _, label := range []string{me, me + " + Pet"} {
			l := local[strings.ToLower(label)]
			if l == nil {
				continue
			}
			r := rows[strings.ToLower(label)]
			if r == nil {
				rows[strings.ToLower(label)] = &RaidDPSPlayerUI{
					Name: l.display, Melee: l.melee, Other: l.other,
				}
				continue
			}
			if l.melee > r.Melee {
				r.Melee = l.melee
			}
			if l.other > r.Other {
				r.Other = l.other
			}
		}
	}

	if secs < 1 {
		secs = 1
	}
	for _, r := range rows {
		r.Total = r.Melee + r.Other
		if r.Total <= 0 {
			continue
		}
		r.SDPS = float64(r.Total) / secs
		out.Total += r.Total
		out.Top = append(out.Top, *r)
	}
	sort.Slice(out.Top, func(i, j int) bool { return out.Top[i].Total > out.Top[j].Total })
	for i := range out.Top {
		if out.Total > 0 {
			out.Top[i].Pct = float64(out.Top[i].Total) / float64(out.Total) * 100
		}
	}
	out.Mob = display
	out.EngagedS = int(secs)
	out.RaidDPS = float64(out.Total) / secs
	return out
}

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
	// Melee Info off: keep parsing locally so this client's own meter still
	// works, but contribute nothing. Their rows simply won't appear on anyone
	// else's board.
	if !GetSettings().MeleeInfo {
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
			Zone:         CurrentZone(),
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
			// Name shape catches charmed mobs ("a Drakkel Dire Wolf"); the
			// speech registry catches the ones named like players, and carries
			// the owner when a tell proved it.
			known, owner := petInfo(name)
			p.Rows = append(p.Rows, rdpsRowWire{
				Name: name, Melee: dmg, EngagedS: eng,
				Backstab: f.backstab[name],
				Pet:      known || strings.ContainsAny(name, " `'"),
				Owner:    owner,
			})
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

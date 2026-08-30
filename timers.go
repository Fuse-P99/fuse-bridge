package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tracker mirrors the server's structured tracker. IsYou marks the linked
// member's own tracker entry (server-computed).
type Tracker struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Ago   string `json:"ago"`
	IsYou bool   `json:"is_you"`
}

// TimerEntry mirrors the server's timer mob entry. ZoneTag is the Gynok zone
// tag ("tov"/"st"/"vp"/"fear") for mobs in zone-grouped tracking zones.
type TimerEntry struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"` // "popped" | "in_window" | "upcoming"
	Detail    string    `json:"detail"`
	Remaining string    `json:"remaining"`
	Trackers  []Tracker `json:"trackers"`
	IsRaid    bool      `json:"is_raid"`
	Raid      *RaidCard `json:"raid"`
	ZoneTag   string    `json:"zone_tag"`
}

// RaidKV mirrors the server's name/value pair. AtMs is when a debuff was last
// cast live (0 = unknown), used for the card's countdown bars.
type RaidKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	AtMs  int64  `json:"at_ms,omitempty"`
}

// RaidCHSlot mirrors a server CH-chain slot. CalledAtMs/InterruptedAtMs feed
// the 10s CH cast bar (0 = unknown / not interrupted).
type RaidCHSlot struct {
	Label           string `json:"label"`
	Cleric          string `json:"cleric"`
	Tank            string `json:"tank"`
	Dead            bool   `json:"dead"`
	CalledAtMs      int64  `json:"called_at_ms,omitempty"`
	InterruptedAtMs int64  `json:"interrupted_at_ms,omitempty"`
	// Stale greys the slot: the cleric moved to another position, missed two
	// chain cycles, or missed their spot while the zone roster last saw them
	// somewhere else. StaleWhy is the row tooltip.
	Stale    bool   `json:"stale,omitempty"`
	StaleWhy string `json:"stale_why,omitempty"`
}

// RaidLoot mirrors the server's auctioned loot item.
type RaidLoot struct {
	Name    string `json:"name"`
	WikiURL string `json:"wiki_url"`
	Price   string `json:"price"`
}

// RaidRaider mirrors a server raider entry.
type RaidRaider struct {
	Name    string `json:"name"`
	Discord string `json:"discord"`
	Level   int    `json:"level"`
}

// RaidClassGroup mirrors a server class group.
type RaidClassGroup struct {
	Class   string       `json:"class"`
	Members []RaidRaider `json:"members"`
}

// RaidRaiders mirrors the server's class composition.
type RaidRaiders struct {
	Total  int              `json:"total"`
	Groups []RaidClassGroup `json:"groups"`
}

// RaidCurrentTarget mirrors a server non-boss add-mob entry.
type RaidCurrentTarget struct {
	Name    string   `json:"name"`
	Debuffs []RaidKV `json:"debuffs"`
	Sieve   int      `json:"sieve"`
}

// RaidTankDisc mirrors one live fighting-style discipline window.
type RaidTankDisc struct {
	Kind string `json:"kind"` // "defensive" | "evasive"
	AtMs int64  `json:"at_ms"`
}

// RaidCard mirrors the server's raid detail card.
type RaidCard struct {
	Target           string              `json:"target"`
	Status           string              `json:"status"`
	KilledAgo        string              `json:"killed_ago"`
	TargetHP         int                 `json:"target_hp"`
	ActiveMainTank   string              `json:"active_main_tank"`
	ActiveRampTank   string              `json:"active_ramp_tank"`
	MainTankList     string              `json:"main_tank_list"`
	TrashTankList    string              `json:"trash_tank_list"`
	RampageTankList  string              `json:"rampage_tank_list"`
	BumpList         string              `json:"bump_list"`
	FlufferClerics   string              `json:"fluffer_clerics"`
	Debuffs          []RaidKV            `json:"debuffs"`
	CHChain          []RaidCHSlot        `json:"ch_chain"`
	Loot             []RaidLoot          `json:"loot"`
	Raiders          RaidRaiders         `json:"raiders"`
	DiscordChannelID string              `json:"discord_channel_id"`
	DiscordURL       string              `json:"discord_url"`
	Sieve            int                 `json:"sieve"`
	CurrentTargets   []RaidCurrentTarget `json:"current_targets"`
	CurrentTanks     []string            `json:"current_tanks"`
	TankProcs        map[string]int      `json:"tank_procs"`
	// TankDiscs holds live fighting-style discipline windows (lower player name
	// → kind + start), for the current and ramp tanks' DEFENSIVE/EVASIVE rows.
	// Mirrors the server's field; see eventTracker.go.
	TankDiscs map[string]RaidTankDisc `json:"tank_discs,omitempty"`
	// Zone is the raid target's home zone; the raid special overlays only
	// render for players standing in it ("" = unknown, fail open).
	Zone string `json:"zone"`
	// Kind distinguishes display types: "" / "mob" is the classic card;
	// "event" is an hourly/wave event raid (Sky / HoT / Ring War) — no health
	// bar, Label as the title, up to 4 equal current targets in a 2×2 grid.
	Kind string `json:"kind"`
	// Label is the event card's title ("Plane of Sky — Hour 2").
	Label string `json:"label"`
	// EventKey identifies the event ("sky" | "hot" | "ringwar") — drives the
	// Other Timers folder filter.
	EventKey string `json:"event_key"`
	// MainAssist is the current MA from a guild "ASSIST - Name" call.
	MainAssist string `json:"main_assist"`
	// OceanStarts are the Ring War ocean kickoff-shout times in epoch ms,
	// indexed 1-3 (0 = not started). Other Timers derives the next-wave and
	// Narandi countdowns from these.
	OceanStarts []int64 `json:"ocean_starts"`
	// DiscordChannels is an event raid's whole channel set (On Time, hours or
	// waves, Narandi). Empty for mob raids, which use DiscordURL.
	DiscordChannels []RaidChannelLink `json:"discord_channels"`
}

// RaidChannelLink mirrors one Discord channel of an event raid's set.
type RaidChannelLink struct {
	Label  string `json:"label"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Logged bool   `json:"logged"`
}

// BatphoneBanner mirrors the server's freeform batphone banner.
type BatphoneBanner struct {
	Text   string `json:"text"`
	SentAt int64  `json:"sent_at"`
}

// TimersData mirrors the server's parsed timers board.
type TimersData struct {
	Verified bool `json:"verified"`
	// NowMs is the server's clock at serve time; fetchTimers measures this
	// machine's skew against it and rewrites the payload's epoch-ms stamps
	// into local-clock terms (applyClockSkew). 0 = older server.
	NowMs          int64            `json:"now_ms"`
	Porter         string           `json:"porter"`
	Logistics      string           `json:"logistics"`
	Idol           string           `json:"idol"`
	Mobs           []TimerEntry     `json:"mobs"`
	Summary        string           `json:"summary"`
	Updated        string           `json:"updated"`
	FetchedAt      int64            `json:"fetched_at"`
	Batphones      []BatphoneBanner `json:"batphones"`
	CompletedRaids []RaidCard       `json:"completed_raids"`
	// GhostRaid is raid state the server could not attach to a mob — a CH chain
	// running during an event (Sky, Halls of Testing, Ring War) or before a
	// batphone lands. It feeds the three raid special overlays only; it is never
	// a raid on the Raids tab. Its Zone is where the guild actually is, so the
	// overlays' in-zone gate works on it exactly as on a real card.
	GhostRaid *RaidCard `json:"ghost_raid"`
	// EventRaid is the live hourly/event raid (Sky / HoT / Ring War): a real
	// raid card rendered at the top of the Raids tab, coexisting with a mob
	// raid. The special overlays prefer the mob raid, then this, then a ghost.
	EventRaid *RaidCard `json:"event_raid"`
	// AETimers are the shared raid-AE cooldown anchors (server raidAE.go):
	// one client inside the AE's radius anchors the countdown for everyone.
	// Mob is the OtherTimers FOLDER_FILTERS key; the countdown ticks
	// client-side from StartedMs + DurMs.
	AETimers []RaidAETimer `json:"ae_timers"`
}

// RaidAETimer mirrors the server's shared raid-AE anchor.
type RaidAETimer struct {
	Mob       string `json:"mob"`
	Label     string `json:"label"`
	StartedMs int64  `json:"started_ms"`
	DurMs     int64  `json:"dur_ms"`
}

// fetchBatphones retrieves current batphone banners (linked members only).
func fetchBatphones() []BatphoneBanner {
	if !IsLinked() {
		return nil
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/batphones", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out struct {
		Batphones []BatphoneBanner `json:"batphones"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Batphones
}

// fetchTimers retrieves the timers board from the server, passing the current
// character so the server can verify Fuse membership.
func fetchTimers(toon string) TimersData {
	var out TimersData
	if !IsLinked() {
		return out
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	u := base + "/timers"
	if toon != "" {
		u += "?toon=" + url.QueryEscape(toon)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return out
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	json.NewDecoder(resp.Body).Decode(&out)
	applyClockSkew(&out)
	return out
}

// applyClockSkew rewrites the server's epoch-ms stamps into this machine's
// clock terms. A PC whose clock runs seconds behind the server otherwise sees
// every server stamp in the future — a raider's CH chain showed the whole
// rotation mid-cast at once with "22s remaining" on 10-second casts — and a
// skewed clock also defeats the overlays' local-first merges, whose same-cast
// windows compare these stamps against locally-heard sightings. Only skew
// beyond the RTT noise floor is corrected, so healthy clients pass through
// bit-identical. Any NEW epoch-ms field added to the payload needs a line
// here.
func applyClockSkew(out *TimersData) {
	if out.NowMs == 0 {
		return // pre-now_ms server: nothing to measure against
	}
	skew := out.NowMs - time.Now().UnixMilli()
	if skew > -2000 && skew < 2000 {
		return
	}
	adjCard := func(c *RaidCard) {
		if c == nil {
			return
		}
		for i := range c.CHChain {
			if c.CHChain[i].CalledAtMs != 0 {
				c.CHChain[i].CalledAtMs -= skew
			}
			if c.CHChain[i].InterruptedAtMs != 0 {
				c.CHChain[i].InterruptedAtMs -= skew
			}
		}
		for i := range c.Debuffs {
			if c.Debuffs[i].AtMs != 0 {
				c.Debuffs[i].AtMs -= skew
			}
		}
		for i := range c.CurrentTargets {
			for j := range c.CurrentTargets[i].Debuffs {
				if c.CurrentTargets[i].Debuffs[j].AtMs != 0 {
					c.CurrentTargets[i].Debuffs[j].AtMs -= skew
				}
			}
		}
		for k, d := range c.TankDiscs {
			if d.AtMs != 0 {
				d.AtMs -= skew
				c.TankDiscs[k] = d
			}
		}
		for i := range c.OceanStarts {
			if c.OceanStarts[i] != 0 {
				c.OceanStarts[i] -= skew
			}
		}
	}
	for i := range out.Mobs {
		adjCard(out.Mobs[i].Raid)
	}
	adjCard(out.GhostRaid)
	adjCard(out.EventRaid)
	for i := range out.CompletedRaids {
		adjCard(&out.CompletedRaids[i])
	}
	for i := range out.AETimers {
		if out.AETimers[i].StartedMs != 0 {
			out.AETimers[i].StartedMs -= skew
		}
	}
}

package main

// Client side of the permanent history searches (Search → Parses / Raids).
// The server keeps every damage parse and completed raid forever; these are
// the first client surfaces that can reach past the live boards' 2-3h window.
// Reads gate on IsLinked() only.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// ParseHistRow mirrors the server's ParseSearchRow.
type ParseHistRow struct {
	ParseID    int     `json:"parse_id"`
	Mob        string  `json:"mob"`
	BattleAtMs int64   `json:"battle_at_ms"`
	DurationS  int     `json:"duration_s"`
	TotalDmg   int     `json:"total_damage"`
	RaidDPS    float64 `json:"raid_dps"`
	ToonCount  int     `json:"toon_count"`
	Parser     string  `json:"parser"`
	ToonDamage int     `json:"toon_damage"`
	ToonDPS    float64 `json:"toon_dps"`
}

// ParseHistDetailRow mirrors the live parse table's columns (DPS = over
// engaged seconds, SDPS = over the whole fight).
type ParseHistDetailRow struct {
	Name     string  `json:"name"`
	Pct      float64 `json:"pct"`
	Total    int     `json:"total"`
	EDPS     float64 `json:"edps"`
	SDPS     float64 `json:"sdps"`
	EngagedS int     `json:"engaged_s"`
	Died     bool    `json:"died"`
	Tank     bool    `json:"tank"`
}

type ParseHistDetail struct {
	ParseID    int                  `json:"parse_id"`
	Mob        string               `json:"mob"`
	BattleAtMs int64                `json:"battle_at_ms"`
	DurationS  int                  `json:"duration_s"`
	TotalDmg   int                  `json:"total_damage"`
	RaidDPS    float64              `json:"raid_dps"`
	Parser     string               `json:"parser"`
	Rows       []ParseHistDetailRow `json:"rows"`
}

// RaidHistRow mirrors the server's RaidHistoryRow. ParseID links the fight's
// damage parse (0 = none); the roster comes from GetRaidAttendance.
type RaidHistRow struct {
	RaidID        int      `json:"raid_id"`
	Mob           string   `json:"mob"`
	Zone          string   `json:"zone"`
	KillTimeMs    int64    `json:"kill_time_ms"`
	StartedAtMs   int64    `json:"started_at_ms"`
	Players       int      `json:"players"`
	Loot          []string `json:"loot"`
	HasAttendance bool     `json:"has_attendance"`
	ParseID       int      `json:"parse_id"`
}

// histGet mirrors the tracksignup error style: friendly plain-text server
// errors surface verbatim; 401 becomes the link nudge.
func histGet(path string, into interface{}) error {
	if !IsLinked() {
		return errors.New("link your Discord account first")
	}
	data, err := botsRequest(http.MethodGet, path, "")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return errors.New("bad server response")
	}
	return nil
}

func histParams(fromMs, toMs int64, offset int) url.Values {
	v := url.Values{}
	if fromMs > 0 {
		v.Set("from_ms", strconv.FormatInt(fromMs, 10))
	}
	if toMs > 0 {
		v.Set("to_ms", strconv.FormatInt(toMs, 10))
	}
	if offset > 0 {
		v.Set("offset", strconv.Itoa(offset))
	}
	return v
}

// SearchParseHistory searches the parse archive by mob name (substring) and/or
// exact toon name; either may be empty. Pages of 25, newest first.
func (a *App) SearchParseHistory(mob, toon string, fromMs, toMs int64, offset int) ([]ParseHistRow, error) {
	v := histParams(fromMs, toMs, offset)
	if mob != "" {
		v.Set("mob", mob)
	}
	if toon != "" {
		v.Set("toon", toon)
	}
	var out struct {
		Parses []ParseHistRow `json:"parses"`
	}
	if err := histGet("/parses/search?"+v.Encode(), &out); err != nil {
		return nil, err
	}
	if out.Parses == nil {
		out.Parses = []ParseHistRow{}
	}
	return out.Parses, nil
}

// GetParseHistDetail returns one archived parse's full per-toon breakdown.
func (a *App) GetParseHistDetail(parseID int) (ParseHistDetail, error) {
	var out ParseHistDetail
	err := histGet("/parses/get?id="+strconv.Itoa(parseID), &out)
	if err != nil {
		return out, err
	}
	if out.Rows == nil {
		out.Rows = []ParseHistDetailRow{}
	}
	return out, nil
}

var (
	memberOkMu sync.Mutex
	memberOk   bool
	memberOkAt time.Time
)

// IsMemberVerified asks the server whether this client's token maps to a live
// member (not revoked, not deleted). Drives which Search sub-tabs are SHOWN —
// every member endpoint re-checks server-side regardless. Fail-closed with a
// short cache: a server blip briefly hides the member sub-tabs, and the next
// check brings them back.
func (a *App) IsMemberVerified() bool {
	if !IsLinked() {
		return false
	}
	memberOkMu.Lock()
	defer memberOkMu.Unlock()
	if time.Since(memberOkAt) < 30*time.Second {
		return memberOk
	}
	memberOk = false
	memberOkAt = time.Now()
	data, err := botsRequest(http.MethodGet, "/ismember", "")
	if err == nil {
		var out struct {
			Member bool `json:"member"`
		}
		if json.Unmarshal(data, &out) == nil {
			memberOk = out.Member
		}
	}
	return memberOk
}

// MemberWhoPurchase / MemberWhoToon / MemberWhoInfo mirror the server's
// /members/get response — the same data the /who Discord command shows.
type MemberWhoPurchase struct {
	Item   string `json:"item"`
	DKP    int    `json:"dkp"`
	DateMs int64  `json:"date_ms"`
}

type MemberWhoToon struct {
	Name     string `json:"name"`
	Class    string `json:"class"`
	Rostered bool   `json:"rostered"`
	Garanel  bool   `json:"garanel"`
}

type MemberWhoInfo struct {
	Handle            string              `json:"handle"`
	DisplayName       string              `json:"display_name"`
	Rostered          bool                `json:"rostered"`
	DateRosteredMs    int64               `json:"date_rostered_ms"`
	DKP               int                 `json:"dkp"`
	RA30              float64             `json:"ra_30"`
	RALife            float64             `json:"ra_life"`
	GuildLastSeenMs   int64               `json:"guild_last_seen_ms"`
	DiscordLastSeenMs int64               `json:"discord_last_seen_ms"`
	AvatarURL         string              `json:"avatar_url"`
	Purchases         []MemberWhoPurchase `json:"purchases"`
	Toons             []MemberWhoToon     `json:"toons"`
}

// MemberSuggestion is one Members-box autocomplete row: Value is looked up,
// Label is shown, Kind is "member" or "toon" (mirrors the server type).
type MemberSuggestion struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// SearchMembers autocompletes Discord handles, display names, and toon names
// (toons labelled with their owner). Picking any resolves the same way /who
// does — handle → display → toon owner.
func (a *App) SearchMembers(q string) ([]MemberSuggestion, error) {
	var out struct {
		Suggestions []MemberSuggestion `json:"suggestions"`
	}
	if err := histGet("/members/search?q="+url.QueryEscape(q), &out); err != nil {
		return nil, err
	}
	if out.Suggestions == nil {
		out.Suggestions = []MemberSuggestion{}
	}
	return out.Suggestions, nil
}

// GetMemberInfo resolves a member the way /who does (handle → display name →
// toon owner) and returns their card.
func (a *App) GetMemberInfo(q string) (MemberWhoInfo, error) {
	var out MemberWhoInfo
	err := histGet("/members/get?q="+url.QueryEscape(q), &out)
	if err != nil {
		return out, err
	}
	if out.Purchases == nil {
		out.Purchases = []MemberWhoPurchase{}
	}
	if out.Toons == nil {
		out.Toons = []MemberWhoToon{}
	}
	return out, nil
}

// SearchRaidHistory searches completed raids by mob and/or zone (substrings);
// both may be empty. Pages of 25, newest first.
func (a *App) SearchRaidHistory(mob, zone string, fromMs, toMs int64, offset int) ([]RaidHistRow, error) {
	v := histParams(fromMs, toMs, offset)
	if mob != "" {
		v.Set("mob", mob)
	}
	if zone != "" {
		v.Set("zone", zone)
	}
	var out struct {
		Raids []RaidHistRow `json:"raids"`
	}
	if err := histGet("/raids/search?"+v.Encode(), &out); err != nil {
		return nil, err
	}
	if out.Raids == nil {
		out.Raids = []RaidHistRow{}
	}
	for i := range out.Raids {
		if out.Raids[i].Loot == nil {
			out.Raids[i].Loot = []string{}
		}
	}
	return out.Raids, nil
}

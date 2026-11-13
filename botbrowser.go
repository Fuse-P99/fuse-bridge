package main

// Client side of the guild bot browser (Search → Bots): the shared "bot"
// characters managed by the King Ak'Anon Discord bot. The FuseBridge server
// proxies these calls to the KA process, which enforces per-member access
// (NOBOTS role, per-bot access roles) itself — so this file is plain reads
// plus one claim action over the usual authenticated channel.
//
// Reads gate on IsLinked() only (never serverForwardOK): a boxer playing on
// Green still browses and claims Blue guild bots.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GuildBot is one roster row. Timestamps are epoch ms (the bindings generator
// does not support time.Time).
type GuildBot struct {
	Name           string   `json:"name"`
	Class          string   `json:"class"`
	Race           string   `json:"race"`
	Level          int      `json:"level"`
	Note           string   `json:"note"`
	ParkZone       string   `json:"park_zone"`
	ParkNote       string   `json:"park_note"`
	ParkTimeMs     int64    `json:"park_time_ms"`
	ClaimedBy      string   `json:"claimed_by"`
	ClaimExpiresMs int64    `json:"claim_expires_ms"`
	Alerts         []string `json:"alerts"`
}

// GuildBotDetail adds the login credentials — returned only by the detail and
// claim calls, never the list.
type GuildBotDetail struct {
	GuildBot
	EQUsername string `json:"eq_username"`
	EQPassword string `json:"eq_password"`
}

// BatphoneBots is the "bots available for the current batphone" surface.
type BatphoneBots struct {
	Mob      string     `json:"mob"`
	SentAtMs int64      `json:"sent_at_ms"`
	Bots     []GuildBot `json:"bots"`
}

// botsRequest performs one authenticated call, surfacing the server's friendly
// plain-text error bodies verbatim (throttle, "already claimed by X", the
// offline notice…) the same way tracksignup does.
func botsRequest(method, path string, body string) ([]byte, error) {
	if !IsLinked() {
		return nil, errors.New("link your Discord account first")
	}
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimSuffix(serverURL, "/submit")+path, rd)
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		text := strings.TrimSpace(string(msg))
		if resp.StatusCode == http.StatusUnauthorized || text == "" || text == "Unauthorized" {
			return nil, errors.New("link your Discord account first")
		}
		return nil, errors.New(text)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

// GetGuildBots returns the member-visible bot roster (no credentials).
func (a *App) GetGuildBots() ([]GuildBot, error) {
	data, err := botsRequest(http.MethodGet, "/bots", "")
	if err != nil {
		return nil, err
	}
	var out struct {
		Bots []GuildBot `json:"bots"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("bad server response")
	}
	if out.Bots == nil {
		out.Bots = []GuildBot{}
	}
	return out.Bots, nil
}

// GetGuildBotDetail returns one bot's full record, credentials included.
func (a *App) GetGuildBotDetail(name string) (GuildBotDetail, error) {
	var d GuildBotDetail
	data, err := botsRequest(http.MethodGet, "/bots/detail?name="+url.QueryEscape(name), "")
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, errors.New("bad server response")
	}
	return d, nil
}

// ClaimGuildBot places a 5-minute claim under the member's Discord name —
// announced in the guild's bots-data channel exactly like a Discord claim —
// and returns the credentials on success.
func (a *App) ClaimGuildBot(name string) (GuildBotDetail, error) {
	var d GuildBotDetail
	body, _ := json.Marshal(map[string]string{"name": name})
	data, err := botsRequest(http.MethodPost, "/bots/claim", string(body))
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, errors.New("bad server response")
	}
	return d, nil
}

// ParkGuildBot applies an inline park edit — the same operation as King
// Ak'Anon's /botpark command (zone synonyms allowed; the old park is
// archived). Returns the server's friendly error text on rejection
// (unknown zone, access, throttle).
func (a *App) ParkGuildBot(name, zone, note string) error {
	body, _ := json.Marshal(map[string]string{"name": name, "zone": zone, "note": note})
	_, err := botsRequest(http.MethodPost, "/bots/park", string(body))
	return err
}

var (
	botZonesMu    sync.Mutex
	botZonesCache []string
)

// GetGuildBotZones returns King Ak'Anon's canonical zone list for the park
// editor. Static data — fetched once per session.
func (a *App) GetGuildBotZones() ([]string, error) {
	botZonesMu.Lock()
	defer botZonesMu.Unlock()
	if botZonesCache != nil {
		return botZonesCache, nil
	}
	data, err := botsRequest(http.MethodGet, "/bots/zones", "")
	if err != nil {
		return nil, err
	}
	var out struct {
		Zones []string `json:"zones"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("bad server response")
	}
	if out.Zones == nil {
		out.Zones = []string{}
	}
	botZonesCache = out.Zones
	return out.Zones, nil
}

var (
	bpBotsCacheMu sync.Mutex
	bpBotsCache   BatphoneBots
	bpBotsCacheAt time.Time
)

// GetBatphoneBots returns the bots relevant to the most recent batphone (empty
// when none in the last 10 minutes). Error-swallowing zero value like
// GetBatphones — this decorates the batphone bar and must never error it.
// Cached briefly: App.svelte polls at 8s and the Bots pane at 15s.
func (a *App) GetBatphoneBots() BatphoneBots {
	bpBotsCacheMu.Lock()
	if time.Since(bpBotsCacheAt) < 5*time.Second {
		cached := bpBotsCache
		bpBotsCacheMu.Unlock()
		return cached
	}
	bpBotsCacheMu.Unlock()

	out := BatphoneBots{Bots: []GuildBot{}}
	data, err := botsRequest(http.MethodGet, "/bots/formob", "")
	if err == nil {
		if json.Unmarshal(data, &out) != nil || out.Bots == nil {
			out = BatphoneBots{Bots: []GuildBot{}}
		}
	}
	bpBotsCacheMu.Lock()
	bpBotsCache = out
	bpBotsCacheAt = time.Now()
	bpBotsCacheMu.Unlock()
	return out
}

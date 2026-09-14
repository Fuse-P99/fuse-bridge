package main

// The Guild Chat overlay's fetch path. The server already relays every guild
// message and raid-mob engage line to Discord, so it holds the whole stream and
// this side never parses the log for it — one authenticated GET returns whatever
// arrived since the last id this client saw.
//
// Officer-only, enforced on the server: a 401/403 comes back as Officer=false
// and the overlay says so rather than looking broken.
//
// Polled by the overlay component alone, and only while it is open, so a closed
// Guild Chat overlay costs nothing — there is no background poller here.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GuildFeedEntry is one line of the stream: a guild message, or a raid-mob
// engage line whose Text is the whole line and whose Toon is the engaged player.
//
// Officer and Bot are the server's answer about the SPEAKER — the same rules
// that give a line its police-officer flair or robot icon in #guild-stream — so
// the overlay draws those marks without knowing the roster itself.
type GuildFeedEntry struct {
	ID      int64  `json:"id"`
	AtMs    int64  `json:"at_ms"`
	Kind    string `json:"kind"`
	Toon    string `json:"toon"`
	Display string `json:"display"`
	Handle  string `json:"handle"`
	Officer bool   `json:"officer"`
	Bot     bool   `json:"bot"`
	Text    string `json:"text"`
	// Items are the item names the server found inside Text — the same matcher
	// that decides which words get a wiki link in Discord. The overlay
	// underlines them and offers the shared item card on hover. A server that
	// predates the field simply omits it, so "no list" must read as "no items
	// on this line", never as an error.
	Items []string `json:"items"`
}

// GuildFeedUI is what the overlay receives.
//
// OK false means the server did not ANSWER — a timeout, a non-200 other than the
// officer refusals, or a body that wouldn't decode — and the overlay keeps the
// lines it already has rather than blanking on a hiccup. Officer false means it
// answered and said no. BootMs is the server's start time: when it changes the
// entry ids have started over, so the overlay drops what it holds and re-reads
// from the beginning. Entries is never nil.
type GuildFeedUI struct {
	OK       bool             `json:"ok"`
	Officer  bool             `json:"officer"`
	BootMs   int64            `json:"boot_ms"`
	LatestID int64            `json:"latest_id"`
	Entries  []GuildFeedEntry `json:"entries"`
}

// GetGuildFeed returns the guild-chat lines newer than after. Bound to the
// frontend.
//
// Linked-only rather than home-world-only on purpose: the feed is the Blue guild
// chat the server holds, so it reads correctly from any world — the same rule the
// raid boards use.
func (a *App) GetGuildFeed(after int64) GuildFeedUI {
	out := GuildFeedUI{Entries: []GuildFeedEntry{}}
	if !IsLinked() {
		// No token, so no request: the server would only refuse it.
		return out
	}
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	// The limit only bites on the backfill — the first fetch of a freshly opened
	// overlay, or one after a server restart. Incremental polls ask for ids past
	// `after` and normally come back with a line or two.
	q.Set("limit", "500")
	u := strings.TrimSuffix(serverURL, "/submit") + "/guildfeed?" + q.Encode()
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
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// The server answered; it just won't serve this member.
		out.OK = true
		return out
	case http.StatusOK:
	default:
		return out
	}
	var body GuildFeedUI
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return out
	}
	body.OK = true
	body.Officer = true
	if body.Entries == nil {
		body.Entries = []GuildFeedEntry{}
	}
	return body
}

package main

// The Guild Chat special overlay's data path — local log first, server second.
//
// Two sources meet in this file. The SERVER holds the whole stream: every
// client's forwarded guild chat, collapsed to one copy per message, each line
// carrying its speaker's Discord identity, the officer/bot marks and the item
// names the wiki matcher found. It is the only side that can know any of that,
// and the only side that sees guildmates' lines at all.
//
// But a line takes a second or three to get there and back — the sender batches,
// this side polls — and a chat window that shows the player their OWN guild chat
// three seconds after the game did reads as broken. So this file also parses
// this client's own guild lines straight out of the log and shows them at once
// as PENDING rows: time, toon, message, and no identity bracket yet. When the
// server's copy of the same line comes back it is merged ONTO that row, which
// keeps its id, its place in the list and its log time while gaining the
// bracket, the marks and the item underlines.
//
// All of that merging happens here so the overlay component stays a renderer.
// The server relationship lives here too: a goroutine polls only while the
// overlay is asking for the view and stops a few seconds after it stops asking,
// so a closed Guild Chat overlay still costs nothing.
//
// Officer-only is enforced on the server: a 401/403 comes back as Officer=false
// and the overlay says so — but only when it has nothing of its own to show,
// since a member's own log lines are theirs to read either way.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	// Pending: this row was read from THIS client's log and the server has not
	// confirmed it yet, so everything only the server can supply — the identity
	// bracket, the officer/bot marks, the item names — is still missing. The
	// overlay draws such a row without the bracket rather than guessing at one.
	Pending bool `json:"pending"`
}

// gcServerResp is the /guildfeed body. Kept separate from GuildFeedUI because
// the two are no longer the same shape: the UI struct describes the merged
// view this file holds, while this one is the server's half of the story.
type gcServerResp struct {
	Entries []GuildFeedEntry `json:"entries"`
	// LatestID is the newest id the server holds, which may be past the last
	// entry it handed back (it filters what a given member may see). Asking for
	// it next time is what keeps a poll from re-reading the same lines forever.
	LatestID int64 `json:"latest_id"`
}

// GuildFeedUI is the merged view as the overlay receives it.
//
// OK false means the server did not ANSWER — a timeout, a non-200 other than
// the officer refusals, or a body that wouldn't decode. Nothing is dropped on
// that: the view is held here and survives any number of failed fetches.
// Officer false means it answered and said no.
//
// Version/Changed are the whole transfer protocol: the overlay hands back the
// version it last rendered, and a view that hasn't moved since answers with
// Changed false and no entries, so a 2-second safety-net poll of a quiet chat
// costs one integer comparison. Entries is never nil.
type GuildFeedUI struct {
	OK      bool             `json:"ok"`
	Officer bool             `json:"officer"`
	Version int64            `json:"version"`
	Changed bool             `json:"changed"`
	Entries []GuildFeedEntry `json:"entries"`
}

// gcIdentity is what the server has told us about a speaker: who owns the toon
// and which marks their lines carry. Held for the session only (memory, never
// written down) so a local line from someone who has already spoken can be
// drawn with its bracket immediately instead of waiting for the round trip.
type gcIdentity struct {
	display string
	handle  string
	officer bool
	bot     bool
}

var (
	gcMu sync.Mutex
	// gcView is the merged list in insertion order, oldest first — the order
	// the overlay renders (it reverses for "Reverse Messages" itself).
	gcView    []GuildFeedEntry
	gcVersion int64 // bumped on every change to gcView
	gcSeenSrv = map[int64]bool{}
	gcAfter   int64 // highest server id fetched, the next poll's `after`
	gcOK      bool
	// Assume officer until the server says otherwise, so the overlay never
	// flashes "Officers only" on its first frame.
	gcOfficer = true
	gcIdent   = map[string]gcIdentity{} // lower(toon) → what the server said
	// gcLocalSeq walks DOWN from zero: local rows get -1, -2, … Server ids are
	// positive, so a local id can never collide with one, and the overlay's
	// keyed list keeps a row's DOM identity when the server's copy merges onto
	// it (the id it was keyed by does not change).
	gcLocalSeq int64
	gcLastAsk  time.Time // when the overlay last asked for the view
	gcPolling  bool      // a poll goroutine is running
)

var (
	// Guild chat as it appears in the player's own log. Self lines keep their
	// "You say to your guild" form here — rewriteSelfGuildSay only runs on the
	// forwarding path — so both shapes are matched. Greedy on purpose: the
	// message's own apostrophes must not end it early, only the last quote does.
	gcGuildRE = regexp.MustCompile(`^(\w+) tells the guild, '(.*)'$`)
	gcSelfRE  = regexp.MustCompile(`^You say to your guild, '(.*)'$`)
)

const (
	// As many lines as the server will hand back in one go. Older ones fall off
	// the top; this is a live stream, not a searchable archive.
	gcMaxRows = 500
	// A line must be this fresh in WALL CLOCK terms to be shown. A client that
	// starts up and tails an existing log replays hours of old chat; those lines
	// are history, not conversation (the same rule raiddps.go applies).
	gcFreshWindow = 30 * time.Second
	// How far apart a local row's log time and the server's stamp for the same
	// message may be and still be the same message. The two clocks are
	// different machines', and a player's PC is routinely minutes off the
	// server — a window measured in seconds would leave that player's own
	// lines unconfirmed forever and then show the server's copies beside them.
	// Ten minutes covers any sane skew, and the server collapses a repeat of
	// the same message inside five minutes anyway, so a second identical line
	// inside this window has no server copy to be confused with.
	gcMatchWindow = 10 * time.Minute
	gcPollEvery   = time.Second
	// A member the server refuses is polled far more slowly — just often enough
	// that someone promoted to officer mid-session starts seeing the stream
	// without restarting the client.
	gcRefusedEvery = 10 * time.Second
	// No one has asked for the view in this long: the overlay is closed (or its
	// window is gone), so the poller stops.
	gcIdleStop = 5 * time.Second
)

// ── local capture ───────────────────────────────────────────────────────────

// RecordGuildChatLocalLine shows this client's own guild chat the instant it is
// logged, without waiting for the round trip through the server. Called for
// every log line, so the cheap content test comes first.
func RecordGuildChatLocalLine(line string) {
	content := logMessageContent(line)
	if !strings.Contains(content, "tells the guild, '") &&
		!strings.HasPrefix(content, "You say to your guild, '") {
		return
	}
	// The feed is the Blue guild's chat. A character on another world is in a
	// different guild entirely, and their guild chat belongs nowhere near it —
	// the same guardrail the forwarding path applies a few lines later.
	if !onHomeServer() {
		return
	}
	at := logLineTime(line)
	if at.IsZero() {
		at = time.Now()
	}
	// Replay guard: a startup tail of an existing log re-reads old chat.
	if d := time.Since(at); d > gcFreshWindow || d < -gcFreshWindow {
		return
	}
	// Only while the overlay is open (someone is asking for the view). A local
	// line is worth holding only for the seconds until the server's copy
	// confirms it; with the overlay closed nothing polls the server, so the
	// pending rows would just pile up — and when the overlay finally opened,
	// its backfill would find them too old to match and show every one of
	// those messages twice. Closed overlay: the server's backfill is the
	// history, and it is complete.
	gcMu.Lock()
	active := time.Since(gcLastAsk) <= gcIdleStop
	gcMu.Unlock()
	if !active {
		return
	}
	var toon, text string
	if m := gcGuildRE.FindStringSubmatch(content); m != nil {
		toon, text = m[1], m[2]
	} else if m := gcSelfRE.FindStringSubmatch(content); m != nil {
		// Our own line names no speaker — the log is written from inside the
		// character. With no character name yet there is nobody to label it.
		if currentCharName == "" {
			return
		}
		toon, text = currentCharName, m[1]
	} else {
		return
	}

	gcMu.Lock()
	gcLocalSeq--
	e := GuildFeedEntry{
		ID:      gcLocalSeq,
		AtMs:    at.UnixMilli(),
		Kind:    "guild",
		Toon:    toon,
		Text:    text,
		Items:   []string{},
		Pending: true,
	}
	// Someone who has already spoken this session can be labelled at once; for
	// anyone else the bracket simply arrives with the server's copy.
	if id, ok := gcIdent[strings.ToLower(toon)]; ok {
		e.Display, e.Handle, e.Officer, e.Bot = id.display, id.handle, id.officer, id.bot
	}
	gcAppendLocked(e)
	gcVersion++
	gcMu.Unlock()
	gcEmitChanged()
}

// ── merged view ─────────────────────────────────────────────────────────────

// gcAppendLocked adds one row and holds the view at its cap, dropping the
// oldest. Caller holds gcMu.
func gcAppendLocked(e GuildFeedEntry) {
	gcView = append(gcView, e)
	if len(gcView) > gcMaxRows {
		gcView = append(gcView[:0:0], gcView[len(gcView)-gcMaxRows:]...)
	}
}

// gcNoteIdentLocked remembers what the server said about a speaker, so the next
// local line from them can be drawn with its bracket straight away. Caller
// holds gcMu.
func gcNoteIdentLocked(e GuildFeedEntry) {
	toon := strings.TrimSpace(e.Toon)
	if toon == "" {
		return
	}
	gcIdent[strings.ToLower(toon)] = gcIdentity{
		display: e.Display, handle: e.Handle, officer: e.Officer, bot: e.Bot,
	}
}

// gcConfirmPendingLocked merges a server entry onto the pending local row for
// the same message, reporting whether it found one. Caller holds gcMu.
//
// The match: same toon (case-insensitively), byte-identical text once both are
// trimmed, and log times within gcMatchWindow of each other. Oldest pending row
// first, so a message someone repeated pairs in the order it was said.
//
// What is KEPT is as important as what is filled in. The row keeps its local id
// — the overlay keys its list by id, so changing it would tear the row out of
// the DOM and build a new one, losing the scroll anchor and any transition
// mid-flight — and it keeps its LOCAL timestamp, which is the truer clock: it
// is when the line was said, while the server's is when the line arrived.
func gcConfirmPendingLocked(srv GuildFeedEntry) bool {
	for i := range gcView {
		p := &gcView[i]
		if !p.Pending || p.Kind != srv.Kind {
			continue
		}
		if d := p.AtMs - srv.AtMs; d > gcMatchWindow.Milliseconds() || d < -gcMatchWindow.Milliseconds() {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(p.Toon), strings.TrimSpace(srv.Toon)) {
			continue
		}
		if strings.TrimSpace(p.Text) != strings.TrimSpace(srv.Text) {
			continue
		}
		p.Display, p.Handle = srv.Display, srv.Handle
		p.Officer, p.Bot = srv.Officer, srv.Bot
		p.Items = srv.Items
		if p.Items == nil {
			p.Items = []string{}
		}
		p.Pending = false
		return true
	}
	return false
}

// gcMergeServer folds one fetch's worth of server entries into the view: each
// one either confirms a pending local row or joins the list on its own.
//
// A pending row the server never confirms stays exactly as it is. That is not
// an oversight: the server collapses a message repeated inside five minutes to
// one copy, so the second "inc" a player types has no server line coming for it
// — and it is still a real line of their own chat, which is what this overlay
// is for.
func gcMergeServer(entries []GuildFeedEntry) {
	if len(entries) == 0 {
		return
	}
	gcMu.Lock()
	changed := false
	for _, e := range entries {
		if e.ID > gcAfter {
			gcAfter = e.ID
		}
		// Identity is learned from every line, confirmations included — that is
		// what lets the NEXT local line from this speaker open with its bracket.
		gcNoteIdentLocked(e)
		if gcSeenSrv[e.ID] {
			continue
		}
		gcSeenSrv[e.ID] = true
		e.Pending = false
		if e.Items == nil {
			e.Items = []string{}
		}
		if e.Kind == "guild" && gcConfirmPendingLocked(e) {
			changed = true
			continue
		}
		gcAppendLocked(e)
		changed = true
	}
	gcPruneSeenLocked()
	if changed {
		gcVersion++
	}
	gcMu.Unlock()
	if changed {
		gcEmitChanged()
	}
}

// gcPruneSeenLocked bounds the merged-id set. Every fetch asks for ids past
// gcAfter, so an id well below it can never be offered again and remembering it
// buys nothing. Caller holds gcMu.
func gcPruneSeenLocked() {
	if len(gcSeenSrv) <= 2*gcMaxRows {
		return
	}
	floor := gcAfter - gcMaxRows
	for id := range gcSeenSrv {
		if id <= floor {
			delete(gcSeenSrv, id)
		}
	}
}

// gcNoteLatest advances the next fetch's starting point to the newest id the
// server holds. Past the newest ENTRY it handed back on purpose: the server
// filters what a member may see, and without this a line it withheld would be
// re-offered (and re-filtered) on every poll forever.
func gcNoteLatest(latest int64) {
	if latest <= 0 {
		return
	}
	gcMu.Lock()
	if latest > gcAfter {
		gcAfter = latest
	}
	gcMu.Unlock()
}

func gcEmitChanged() {
	if v3App != nil {
		v3App.Event.Emit("guildchat-changed")
	}
}

// ── the binding ─────────────────────────────────────────────────────────────

// GetGuildFeed returns the merged guild-chat view when it has moved since the
// version the caller last rendered, and nothing but that version when it has
// not. Bound to the frontend.
//
// Asking is also what keeps the server poller alive: the overlay is the only
// thing that wants this data, so the fetching runs while it is asking and stops
// shortly after it stops.
func (a *App) GetGuildFeed(version int64) GuildFeedUI {
	gcMu.Lock()
	gcLastAsk = time.Now()
	start := !gcPolling
	if start {
		gcPolling = true
	}
	out := GuildFeedUI{
		OK:      gcOK,
		Officer: gcOfficer,
		Version: gcVersion,
		Entries: []GuildFeedEntry{},
	}
	if version != gcVersion {
		out.Changed = true
		// A copy: the poller may append to gcView while this is being marshalled.
		out.Entries = make([]GuildFeedEntry, len(gcView))
		copy(out.Entries, gcView)
	}
	gcMu.Unlock()
	if start {
		go gcPollLoop()
	}
	return out
}

// gcPollLoop fetches the server's half of the stream for as long as the overlay
// is reading it. One goroutine at a time, started by the first GetGuildFeed and
// gone a few seconds after the last one.
func gcPollLoop() {
	defer func() {
		gcMu.Lock()
		gcPolling = false
		gcMu.Unlock()
	}()
	for {
		gcMu.Lock()
		quiet := time.Since(gcLastAsk)
		gcMu.Unlock()
		if quiet > gcIdleStop {
			return
		}
		next := gcPollEvery
		// Unlinked: no token, so the server would only refuse it. Keep ticking
		// rather than exiting — the local lines still flow, and linking mid-
		// session then starts fetching without the overlay being reopened.
		if IsLinked() {
			next = gcFetchOnce()
		}
		time.Sleep(next)
	}
}

// gcFetchOnce does one GET and merges what comes back, returning how long to
// wait before the next one. A failed fetch changes nothing but gcOK: the view
// is held here, so a hiccup leaves the overlay showing what it was showing.
func gcFetchOnce() time.Duration {
	gcMu.Lock()
	after := gcAfter
	gcMu.Unlock()
	q := url.Values{}
	// Zero on the first fetch of the session, which is the backfill; every later
	// one asks for what is past the newest id already merged.
	q.Set("after", strconv.FormatInt(after, 10))
	// The limit only bites on that backfill. Incremental polls normally come
	// back with a line or two.
	q.Set("limit", "500")
	u := strings.TrimSuffix(serverURL, "/submit") + "/guildfeed?" + q.Encode()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		gcSetState(false, gcOfficerNow())
		return gcPollEvery
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		gcSetState(false, gcOfficerNow())
		return gcPollEvery
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// The server answered; it just won't serve this member.
		gcSetState(true, false)
		return gcRefusedEvery
	case http.StatusOK:
	default:
		gcSetState(false, gcOfficerNow())
		return gcPollEvery
	}
	var body gcServerResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		gcSetState(false, gcOfficerNow())
		return gcPollEvery
	}
	gcMergeServer(body.Entries)
	gcNoteLatest(body.LatestID)
	gcSetState(true, true)
	return gcPollEvery
}

// gcSetState records the last server answer. Deliberately NOT versioned: the
// overlay is handed ok/officer on every call, changed view or not, so a member
// promoted mid-session is told without the line list having to move.
func gcSetState(ok, officer bool) {
	gcMu.Lock()
	gcOK, gcOfficer = ok, officer
	gcMu.Unlock()
}

// gcOfficerNow is the officer verdict to carry forward when a fetch fails —
// the last one the server gave, since a timeout says nothing about membership.
func gcOfficerNow() bool {
	gcMu.Lock()
	defer gcMu.Unlock()
	return gcOfficer
}

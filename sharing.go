package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// User-to-user sharing: send a single trigger or a set of map markers to any
// other client, linked or not. Identity is a persistent anonymous credential
// (Settings.ShareSecret) registered with the server for a short public addr.
// Incoming shares queue server-side until accepted or declined here — nothing
// is ever auto-applied. See shareHandler.go on the server.

// ShareInboxItem is one pending incoming share, as served by /share/inbox.
type ShareInboxItem struct {
	ID         int    `json:"id"`
	FromAddr   string `json:"from_addr"`
	FromName   string `json:"from_name"`
	FromLinked bool   `json:"from_linked"` // sender was Discord-linked (verified name)
	Kind       string `json:"kind"`        // "trigger" | "markers"
	Meta       string `json:"meta"`        // server-generated preview JSON
	Payload    string `json:"payload"`
	CreatedMs  int64  `json:"created_ms"`
}

var (
	shareMu    sync.Mutex
	shareInbox []ShareInboxItem
	shareIDs   string // fingerprint of the cached inbox, to emit only on change
)

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// shareRequest performs an authenticated share API call. The X-Share-Secret
// header is the credential; the bearer header rides along when linked so the
// server can attach (and badge) the member identity on register.
func shareRequest(method, path string, body any, out any) error {
	var rdr *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, registerBase()+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Share-Secret", GetSettings().ShareSecret)
	if h := authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The share endpoints put a human-readable reason in the body (e.g.
		// "share too large", "unknown recipient") — surface it to the UI.
		var msg [256]byte
		n, _ := resp.Body.Read(msg[:])
		reason := string(bytes.TrimSpace(msg[:n]))
		if reason == "" {
			reason = fmt.Sprintf("server returned %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", reason)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ensureShareRegistered mints the secret on first use and (re)registers with
// the server whenever we have no addr yet or the tailed toon changed since the
// last registration. The secret and addr are stable across renames.
func ensureShareRegistered() error {
	s := GetSettings()
	if s.ShareSecret == "" {
		s.ShareSecret = randomHex(32) // 64 hex chars
		UpdateSettings(s)
	}
	name := currentCharName
	if name == "" {
		name = s.ShareName // no toon tailed yet — keep whatever we last had
	}
	if s.ShareAddr != "" && name == s.ShareName {
		return nil
	}
	var out struct {
		Addr   string `json:"addr"`
		Linked bool   `json:"linked"`
	}
	err := shareRequest(http.MethodPost, "/share/register",
		map[string]string{"secret": s.ShareSecret, "name": name}, &out)
	if err != nil {
		return err
	}
	s = GetSettings()
	if out.Addr != "" {
		s.ShareAddr = out.Addr
	}
	s.ShareName = name
	UpdateSettings(s)
	return nil
}

func fetchShareInbox() ([]ShareInboxItem, error) {
	var out struct {
		Shares []ShareInboxItem `json:"shares"`
	}
	if err := shareRequest(http.MethodGet, "/share/inbox", nil, &out); err != nil {
		return nil, err
	}
	if out.Shares == nil {
		out.Shares = []ShareInboxItem{}
	}
	return out.Shares, nil
}

func shareResolveRemote(id int, action string) error {
	return shareRequest(http.MethodPost, "/share/resolve",
		map[string]any{"id": id, "action": action}, nil)
}

// setShareInbox replaces the cache and, when the contents actually changed,
// notifies the frontend (footer inbox badge) with the new count.
func setShareInbox(items []ShareInboxItem) {
	ids := ""
	for _, it := range items {
		ids += fmt.Sprintf("%d,", it.ID)
	}
	shareMu.Lock()
	changed := ids != shareIDs
	shareInbox = items
	shareIDs = ids
	shareMu.Unlock()
	if changed && v3App != nil {
		v3App.Event.Emit("share-inbox", len(items))
	}
}

// removeShareFromCache drops one item locally (after a resolve) and refreshes
// the badge without waiting for the next poll.
func removeShareFromCache(id int) {
	shareMu.Lock()
	kept := shareInbox[:0]
	ids := ""
	for _, it := range shareInbox {
		if it.ID != id {
			kept = append(kept, it)
			ids += fmt.Sprintf("%d,", it.ID)
		}
	}
	shareInbox = kept
	shareIDs = ids
	n := len(kept)
	shareMu.Unlock()
	if v3App != nil {
		v3App.Event.Emit("share-inbox", n)
	}
}

func cachedShareItem(id int) (ShareInboxItem, bool) {
	shareMu.Lock()
	defer shareMu.Unlock()
	for _, it := range shareInbox {
		if it.ID == id {
			return it, true
		}
	}
	return ShareInboxItem{}, false
}

// startSharePoller keeps the share identity registered (including toon-rename
// re-registers) and polls the inbox once a minute, pushing badge updates to
// the frontend. Modeled on startHeartbeat.
func startSharePoller() {
	go func() {
		for {
			if err := ensureShareRegistered(); err == nil {
				if items, err := fetchShareInbox(); err == nil {
					setShareInbox(items)
				}
			}
			time.Sleep(60 * time.Second)
		}
	}()
}

package main

// Raid-role signup + role guide links for the Raids tab. Signups post through
// the server's /track endpoint, which relays them to Gynok in
// #bot-command-space and returns Gynok's actual reply; guide links are the
// Learn menu's one-Discord-post-per-(mob, role) store.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// trackResponse mirrors the server's /track reply.
type trackResponse struct {
	OK        bool   `json:"ok"`
	Confirmed bool   `json:"confirmed"`
	Message   string `json:"message"`
}

// trackPost sends an authenticated signup action to /track. The timeout is
// generous because the server itself blocks up to ~9s waiting on Gynok's
// reply (stop + start on a switch).
func trackPost(body map[string]any) (trackResponse, error) {
	var out trackResponse
	data, err := json.Marshal(body)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequest(http.MethodPost, registerBase()+"/track", strings.NewReader(string(data)))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return out, fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The server sends friendly plain-text bodies (throttle, unlinked
		// Discord, officer gate…) — surface them as-is.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		text := strings.TrimSpace(string(msg))
		if resp.StatusCode == http.StatusUnauthorized || text == "" || text == "Unauthorized" {
			return out, fmt.Errorf("link your Discord account first")
		}
		return out, errors.New(text)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("bad server response")
	}
	return out, nil
}

// StartTracking signs the linked member up for a role on an in-window mob via
// Gynok (!track <mob> start <role>). switching stops their current tracker
// first — Gynok allows one active tracker per member. Returns Gynok's
// confirmation text (or a "sent, awaiting confirmation" note); Gynok-rejected
// signups come back as errors so the UI shows them in the failure style.
func (a *App) StartTracking(mob, role string, switching bool) (string, error) {
	resp, err := trackPost(map[string]any{"mob": mob, "role": role, "switch": switching})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", errors.New(resp.Message)
	}
	return resp.Message, nil
}

// StopTracking clears the member's active tracker signup (!track stop).
func (a *App) StopTracking() (string, error) {
	resp, err := trackPost(map[string]any{"stop": true})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", errors.New(resp.Message)
	}
	return resp.Message, nil
}

// StopTrackerFor force-stops another member's tracker on a mob — officers
// only, enforced server-side (the server resolves the tracker's identity from
// its board snapshot; clients never see Discord ids).
func (a *App) StopTrackerFor(mob, trackerName string) (string, error) {
	resp, err := trackPost(map[string]any{"stop": true, "mob": mob, "target_name": trackerName})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", errors.New(resp.Message)
	}
	return resp.Message, nil
}

// ---- Role guide links (Learn menu) ----

// TrackGuide is one saved "how to do this role on this mob" Discord post link.
type TrackGuide struct {
	Mob  string `json:"mob"`
	Role string `json:"role"`
	URL  string `json:"url"`
}

var (
	trackGuideMu    sync.Mutex
	trackGuideCache []TrackGuide
	trackGuideAt    time.Time
)

// GetTrackGuides returns all saved guide links (short cache — the Raids tab
// remounts on every tab switch).
func (a *App) GetTrackGuides() []TrackGuide {
	trackGuideMu.Lock()
	if trackGuideCache != nil && time.Since(trackGuideAt) < 5*time.Minute {
		out := trackGuideCache
		trackGuideMu.Unlock()
		return out
	}
	trackGuideMu.Unlock()

	guides := []TrackGuide{}
	req, err := http.NewRequest(http.MethodGet, registerBase()+"/trackguides", nil)
	if err != nil {
		return guides
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return guides
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return guides
	}
	var out struct {
		Guides []TrackGuide `json:"guides"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return guides
	}
	if out.Guides != nil {
		guides = out.Guides
	}
	trackGuideMu.Lock()
	trackGuideCache, trackGuideAt = guides, time.Now()
	trackGuideMu.Unlock()
	return guides
}

// SaveTrackGuide stores the guide link for a (mob, role) pairing — one slot
// per pairing, newest write wins (the UI warns about the overwrite).
func (a *App) SaveTrackGuide(mob, role, url string) error {
	data, err := json.Marshal(TrackGuide{Mob: mob, Role: role, URL: url})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, registerBase()+"/trackguides", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		text := strings.TrimSpace(string(msg))
		if resp.StatusCode == http.StatusUnauthorized || text == "" {
			return fmt.Errorf("link your Discord account first")
		}
		return errors.New(text)
	}
	trackGuideMu.Lock()
	trackGuideCache = nil // refetch on next read
	trackGuideMu.Unlock()
	return nil
}

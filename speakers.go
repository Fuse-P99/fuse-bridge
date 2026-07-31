package main

// Polls the server's /speakers endpoint (who is talking in the guild voice
// channel, and how many people are in it) and pushes changes to the frontend
// footer indicator. Linked clients only — the endpoint needs a bearer token.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type VoiceSpeaker struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Avatar  string `json:"avatar"`
	SinceMs int64  `json:"since_ms"`
}

type SpeakersSnapshot struct {
	Speakers  []VoiceSpeaker `json:"speakers"`
	InChannel int            `json:"in_channel"`
	// ChannelURL is the guild voice channel's web link, for the footer. Empty
	// when the server has no voice channel configured.
	ChannelURL string `json:"channel_url"`
}

var (
	speakersMu   sync.Mutex
	speakersSnap SpeakersSnapshot
	speakersFp   string
)

// GetSpeakers returns the last-known voice snapshot (frontend seed; live
// updates arrive via the "speakers" event).
func (a *App) GetSpeakers() SpeakersSnapshot {
	speakersMu.Lock()
	defer speakersMu.Unlock()
	s := speakersSnap
	if s.Speakers == nil {
		s.Speakers = []VoiceSpeaker{}
	}
	return s
}

func startSpeakerPoller() {
	go func() {
		for range time.Tick(1 * time.Second) {
			pollSpeakers()
		}
	}()
}

func pollSpeakers() {
	if !IsLinked() {
		publishSpeakers(SpeakersSnapshot{Speakers: []VoiceSpeaker{}})
		return
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/speakers", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return // transient network trouble: keep the last state
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var snap SpeakersSnapshot
	if json.NewDecoder(resp.Body).Decode(&snap) != nil {
		return
	}
	publishSpeakers(snap)
}

// publishSpeakers stores the snapshot and emits an event only when the set of
// speakers (or the in-channel count) actually changed, so the 1s poll doesn't
// spam the frontend.
func publishSpeakers(snap SpeakersSnapshot) {
	if snap.Speakers == nil {
		snap.Speakers = []VoiceSpeaker{}
	}
	fp := fmt.Sprintf("%d|%s|", snap.InChannel, snap.ChannelURL)
	for _, s := range snap.Speakers {
		fp += s.ID + ","
	}
	speakersMu.Lock()
	changed := fp != speakersFp
	speakersSnap = snap
	speakersFp = fp
	speakersMu.Unlock()
	if changed && v3App != nil {
		v3App.Event.Emit("speakers", snap)
	}
}

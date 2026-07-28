package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Attendance logs, fetched from the server for the user to paste into a raid
// channel by hand.
//
// The server posts these automatically when it can resolve the DKP channel; this
// is the escape hatch for when it can't. The payload is built server-side (see
// attendance.go in the bot) so a pasted message is byte-identical to a posted
// one — the client only chunks them into buttons and copies to the clipboard.

// AttendanceChunk is one pasteable Discord message.
type AttendanceChunk struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	Lines int    `json:"lines"`
	Kind  string `json:"kind"` // "who" | "presence" | "command"
}

// AttendanceSet mirrors the server's response.
type AttendanceSet struct {
	Zone         string            `json:"zone"`
	Mob          string            `json:"mob"`
	Players      int               `json:"players"`
	CapturedAtMs int64             `json:"captured_at_ms"`
	AgeSecs      int               `json:"age_secs"`
	Snapshot     bool              `json:"snapshot"`
	TodMs        int64             `json:"tod_ms"`
	OffsetSecs   int               `json:"offset_secs"`
	Chunks       []AttendanceChunk `json:"chunks"`
	Error        string            `json:"error"`
}

// fetchAttendance performs the GET and decodes the set. query is the already-
// escaped query string ("zone=..." or "raid=...").
func fetchAttendance(query string) (AttendanceSet, error) {
	var out AttendanceSet
	if !IsLinked() {
		return out, fmt.Errorf("link your Discord account first")
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/attendance?"+query, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return out, fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return out, fmt.Errorf("link your Discord account first")
	default:
		return out, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	if out.Chunks == nil {
		out.Chunks = []AttendanceChunk{}
	}
	return out, nil
}

// GetZoneAttendance builds the attendance log for everyone <Fuse> currently
// tracked in a zone, stamped with the time of the call. Used by the Zones tab.
func (a *App) GetZoneAttendance(zone string) (AttendanceSet, error) {
	if strings.TrimSpace(zone) == "" {
		return AttendanceSet{}, fmt.Errorf("no zone selected")
	}
	return fetchAttendance("zone=" + url.QueryEscape(zone))
}

// GetRaidAttendance returns a completed raid's stored attendance snapshot when
// raidID is set, else the live log for the raid's zone (an active raid has no
// ToD to anchor a snapshot to, so it reads the same as a Zones-tab request).
// Callers pass raidID only for a COMPLETED raid — an active raid has a row but
// no snapshot, and would come back empty.
func (a *App) GetRaidAttendance(raidID int, zone string) (AttendanceSet, error) {
	if raidID > 0 {
		return fetchAttendance("raid=" + strconv.Itoa(raidID))
	}
	return a.GetZoneAttendance(zone)
}

// RaidChannelOption is one entry in the autosend dropdown.
type RaidChannelOption struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedMs int64  `json:"created_ms"`
}

// GetRaidChannels lists the 20 most recently created DKP raid channels for the
// autosend picker. Officer-only server-side; a non-officer gets an empty list
// rather than an error so the UI can simply not show the control.
func (a *App) GetRaidChannels() []RaidChannelOption {
	if !IsLinked() {
		return []RaidChannelOption{}
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/raidchannels", nil)
	if err != nil {
		return []RaidChannelOption{}
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return []RaidChannelOption{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return []RaidChannelOption{}
	}
	var out struct {
		Channels []RaidChannelOption `json:"channels"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.Channels == nil {
		return []RaidChannelOption{}
	}
	return out.Channels
}

// SendAttendance posts an attendance log to a raid channel via the bot, skipping
// the clipboard entirely. Officer-only. Pass raidID for a completed raid's
// snapshot, or zone for a live capture. Returns how many messages were queued.
func (a *App) SendAttendance(channelID string, raidID int, zone string) (int, error) {
	if !IsLinked() {
		return 0, fmt.Errorf("link your Discord account first")
	}
	if strings.TrimSpace(channelID) == "" {
		return 0, fmt.Errorf("pick a raid channel first")
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string]any{
		"channel_id": channelID, "raid": raidID, "zone": zone,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/attendance/send", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return 0, fmt.Errorf("officers only")
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		if t := strings.TrimSpace(string(msg)); t != "" {
			return 0, fmt.Errorf("%s", t)
		}
		return 0, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var out struct {
		Sent int `json:"sent"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	addStatus("Attendance: sent %d message(s) to the raid channel", out.Sent)
	return out.Sent, nil
}

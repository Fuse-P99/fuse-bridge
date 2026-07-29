package main

import (
	"strings"
	"sync"
	"time"
)

// Boat trip recorder — an admin calibration tool for boats the schedule can't
// see.
//
// The Bloated Belly (Timorous Deep ↔ Overthere) makes no dock announcement on
// the Timorous side, so there is nothing for RecordBoatLine to anchor on and no
// way to measure its loop except by riding it. This records the two things that
// ARE observable from a rider's log — zone transitions, and a marker phrase the
// rider types at each dock — stamped with the log's own timestamps.
//
// Entirely client-side and in-memory: it drives no timer, posts nothing to the
// server, and is discarded when the app closes. It is a measuring tape, not a
// feature of the board.
//
// Both clocks are kept per event, because neither alone is good enough:
//
//   - AtMs is EQ's own timestamp. Authoritative about when the event happened,
//     but truncated to whole seconds.
//   - SeenMs is when this process read the line, ~100ms behind EQ writing it
//     (the tailer polls at that interval). Sub-second, but carries the read
//     delay.
//
// Read the intervals off AtMs; SeenMs is there to break ties and to show when
// two events landed inside the same log second.

// boatTrackMax caps retained events so a recording left running overnight can't
// grow without bound. A round trip produces a handful of events, so this is
// thousands of trips.
const boatTrackMax = 2000

// BoatTrackEvent is one observed moment during a recording.
type BoatTrackEvent struct {
	// Kind is "zone" (a "You have entered" transition) or "mark" (the rider's
	// marker phrase).
	Kind   string `json:"kind"`
	Text   string `json:"text"`    // zone name, or the matched log message
	AtMs   int64  `json:"at_ms"`   // EQ log timestamp; 0 if the line had none
	SeenMs int64  `json:"seen_ms"` // when this client read the line
}

// BoatTrackData is the recorder's state, handed to the admin UI.
type BoatTrackData struct {
	Recording bool             `json:"recording"`
	Marker    string           `json:"marker"`
	StartedMs int64            `json:"started_ms"`
	Events    []BoatTrackEvent `json:"events"`
	// Truncated reports that boatTrackMax was hit and the oldest events were
	// dropped, so a gap in the numbers is explained rather than mysterious.
	Truncated bool `json:"truncated"`
}

var (
	btMu        sync.Mutex
	btRecording bool
	btMarker    string
	btStartedMs int64
	btEvents    []BoatTrackEvent
	btTruncated bool
)

// RecordBoatTrackLine feeds one raw log line to the recorder. Called for every
// tailed line, so it early-outs on the common case of not recording.
func RecordBoatTrackLine(line string) {
	btMu.Lock()
	recording, marker := btRecording, btMarker
	btMu.Unlock()
	if !recording {
		return
	}

	// EQ's stamp when the line carries one, else our read time — an event with
	// no clock at all would be worse than one with a slightly late clock.
	seen := time.Now().UnixMilli()
	atMs := seen
	if lt := logLineTime(line); !lt.IsZero() {
		atMs = lt.UnixMilli()
	}

	if zone := ExtractZone(line); zone != "" {
		btAppend(BoatTrackEvent{Kind: "zone", Text: canonicalZone(zone), AtMs: atMs, SeenMs: seen})
		return
	}

	if marker == "" {
		return
	}
	msg := logMessageContent(line)
	if msg == "" {
		return
	}
	// Substring, case-insensitive, against the whole message: the rider's hotkey
	// might be a /say, a /gu or an emote, and each wraps the phrase differently.
	// Matching the phrase itself means the hotkey can be whatever is convenient
	// without this needing to know.
	if strings.Contains(strings.ToLower(msg), strings.ToLower(marker)) {
		btAppend(BoatTrackEvent{Kind: "mark", Text: msg, AtMs: atMs, SeenMs: seen})
	}
}

func btAppend(e BoatTrackEvent) {
	btMu.Lock()
	defer btMu.Unlock()
	btEvents = append(btEvents, e)
	if len(btEvents) > boatTrackMax {
		btEvents = append([]BoatTrackEvent(nil), btEvents[len(btEvents)-boatTrackMax:]...)
		btTruncated = true
	}
}

// StartBoatTrack begins (or restarts) a recording. An empty marker records zone
// transitions only. Previously recorded events are kept — stopping and starting
// again continues the same measurement rather than throwing away a trip.
func StartBoatTrack(marker string) {
	btMu.Lock()
	defer btMu.Unlock()
	btMarker = strings.TrimSpace(marker)
	btRecording = true
	if btStartedMs == 0 {
		btStartedMs = time.Now().UnixMilli()
	}
}

// StopBoatTrack pauses recording, leaving the events in place to read off.
func StopBoatTrack() {
	btMu.Lock()
	btRecording = false
	btMu.Unlock()
}

// ClearBoatTrack discards everything and stops.
func ClearBoatTrack() {
	btMu.Lock()
	btRecording, btEvents, btStartedMs, btTruncated = false, nil, 0, false
	btMu.Unlock()
}

// GetBoatTrackData snapshots the recorder for the UI.
func GetBoatTrackData() BoatTrackData {
	btMu.Lock()
	defer btMu.Unlock()
	return BoatTrackData{
		Recording: btRecording,
		Marker:    btMarker,
		StartedMs: btStartedMs,
		Events:    append([]BoatTrackEvent(nil), btEvents...),
		Truncated: btTruncated,
	}
}

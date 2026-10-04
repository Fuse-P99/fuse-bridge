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

// btLoadPairWindow bounds how long a pending "LOADING" waits for the zone line
// that names it. A load that never completes (crash, alt-F4 on the load screen)
// must not pair with the next unrelated zone change an hour later.
const btLoadPairWindow = 5 * time.Minute

// BoatTrackEvent is one observed moment during a recording.
type BoatTrackEvent struct {
	// Kind is "load" (the zone transition instant — the precise lap marker),
	// "zone" (the "You have entered" that completes it), "announce" (a dock
	// shout) or "mark" (the rider's marker phrase).
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
	// A seen "LOADING" waiting for the zone line that names its destination.
	btPendingLoadAt   int64
	btPendingLoadSeen int64
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

	// "LOADING, PLEASE WAIT..." is the zone transition itself — the instant the
	// server hands the character across, driven by the boat's position. It's
	// held until the following "You have entered", which names the destination.
	if IsZoneLoadingLine(line) {
		btMu.Lock()
		if btPendingLoadAt == 0 {
			btPendingLoadAt, btPendingLoadSeen = atMs, seen
		}
		btMu.Unlock()
		return
	}

	if zone := ExtractZone(line); zone != "" {
		z := canonicalZone(zone)
		btMu.Lock()
		loadAt, loadSeen := btPendingLoadAt, btPendingLoadSeen
		btPendingLoadAt, btPendingLoadSeen = 0, 0
		btMu.Unlock()

		// The precise lap marker. "You have entered" fires only after the client
		// has finished loading the zone, so it carries however long that took —
		// and that varies with disk, memory pressure and whether EQ is in the
		// foreground. Measured against a real run, that variance was ~300ms RMS
		// and swamped everything else; the transition instant itself is stable
		// to about the tailer's poll interval. Anchoring laps here instead is
		// worth roughly a tenfold reduction in the laps needed for a given
		// accuracy.
		//
		// Emitted alongside the "zone" row rather than instead of it, so the two
		// series sit side by side in the summary and the difference is visible
		// rather than asserted.
		if loadAt != 0 && seen-loadSeen < int64(btLoadPairWindow/time.Millisecond) {
			btAppend(BoatTrackEvent{Kind: "load", Text: z, AtMs: loadAt, SeenMs: loadSeen})
		}
		btAppend(BoatTrackEvent{Kind: "zone", Text: z, AtMs: atMs, SeenMs: seen})
		return
	}

	msg := logMessageContent(line)
	if msg == "" {
		return
	}

	// The dock announcements are the frame every schedule offset is measured
	// FROM — dockOffsetS is announcement→dock. A recording that captured only
	// arrivals could measure the loop period but not the offsets, which is the
	// half of the schedule a route like the Bloated Belly is missing.
	for _, a := range boatAnnouncements {
		if strings.HasPrefix(msg, a.prefix) {
			btAppend(BoatTrackEvent{
				Kind: "announce", Text: a.boat + " @ " + a.zone, AtMs: atMs, SeenMs: seen})
			return
		}
	}

	if marker == "" {
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
	btPendingLoadAt, btPendingLoadSeen = 0, 0
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

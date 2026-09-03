package main

import (
	"strings"
	"sync"
	"time"
)

// /who forwarding gate: a /who block only leaves the client once its footer
// proves it describes the LOCAL zone. The server's zone roster and raid
// attendance are built from these lines, and a /who all (or /who all guild)
// snapshot poisons both — it lists characters standing in OTHER zones as if
// they were sightings here.
//
// The accepted signature is a block ENDING in "There are N players in
// <Zone>." where <Zone> is a real zone:
//
//   - footer naming a real zone → the buffered block + footer forward;
//   - footer naming "EverQuest" → /who all — the whole block is discarded;
//   - an entry line carrying a trailing zone ("... ZONE: oasis") is /who-all
//     output whatever the footer claims — it poisons the block;
//   - no footer inside whoGateWindow ("your who request was cut short", a
//     filtered /who with no matches) → discarded. No footer, no collection.
//
// Lines are buffered raw and flushed in order; the log timestamps inside them
// are what the server reads, so the second or two of buffering is invisible.
// Non-who lines never touch the gate — an interleaved combat line neither
// forwards with the block nor invalidates it.

const (
	// whoGateWindow bounds how long a block may take to complete. EQ writes a
	// /who as one burst, so anything slower than this is two separate events.
	whoGateWindow = 3 * time.Second
	// whoGateMax caps the buffer; a full local /who tops out well under this.
	whoGateMax = 300
)

var (
	whoGateMu  sync.Mutex
	whoGateBuf []string
	whoGateBad bool // this block proved to be /who all — swallow to its footer
	whoGateAt  time.Time
)

// whoGateFeed consumes one log line. isWho reports whether the line was /who
// output (the caller must not also forward it through the normal path);
// flush is the accepted block to forward now — nil for everything except the
// footer line that completes a local-zone block.
func whoGateFeed(line string) (flush []string, isWho bool) {
	if !whoPattern.MatchString(line) {
		return nil, false
	}
	if !GetSettings().WhoOutput {
		return nil, true // /who forwarding is off: swallow, forward nothing
	}
	now := time.Now()
	content := logMessageContent(line)

	whoGateMu.Lock()
	defer whoGateMu.Unlock()

	// A block left dangling past the window never got its footer — per the
	// acceptance rule it is not collected. The poison flag ages out the same
	// way, so a cut-short /who all can't swallow a later legitimate /who.
	if (len(whoGateBuf) > 0 || whoGateBad) && now.Sub(whoGateAt) > whoGateWindow {
		whoGateBuf, whoGateBad = nil, false
	}

	if m := whoFooterZonePattern.FindStringSubmatch(content); m != nil {
		zone := strings.TrimSpace(m[1])
		ok := !whoGateBad && !strings.EqualFold(zone, "EverQuest")
		buf := whoGateBuf
		whoGateBuf, whoGateBad = nil, false
		if !ok {
			return nil, true
		}
		return append(buf, line), true
	}

	// /who all entries name each player's zone; a local /who's never do.
	if strings.Contains(content, " ZONE: ") {
		whoGateBuf, whoGateBad, whoGateAt = nil, true, now
		return nil, true
	}
	if whoGateBad {
		whoGateAt = now // still inside the poisoned block
		return nil, true
	}
	if len(whoGateBuf) == 0 {
		whoGateAt = now
	}
	if len(whoGateBuf) < whoGateMax {
		whoGateBuf = append(whoGateBuf, line)
	}
	return nil, true
}

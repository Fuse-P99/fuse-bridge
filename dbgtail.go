package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dbgQuitMarkers are the lines EQ writes to Logs\dbg.txt when the player leaves
// the world deliberately: /q or /quit ("Quit", back to the login screen) and
// /exit ("Exit", straight to desktop) — functionally the same for us. Unlike
// camping out (a countdown in the eqlog), these produce no "prepare your camp"
// line, so on their own the eqlog gives the engine nothing to go on. These
// debug lines are a definitive, immediate "leaving the world" signal — seeing
// one lets us freeze the auto-pause timers and hide the overlays right away.
// The idle timeout stays as the fallback for exits that write no marker at all
// (crash, link-dead, alt-F4).
var dbgQuitMarkers = []string{
	"DISCONNECTING: Quit command received",
	"DISCONNECTING: Exit command received",
}

// isDbgQuitLine reports whether a dbg.txt line is one of the deliberate-quit
// disconnects (not a link-dead / server-initiated drop, where the player may
// reconnect and would want their overlays back immediately).
func isDbgQuitLine(line string) bool {
	for _, m := range dbgQuitMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// dbgCampMarker is the line EQ writes to dbg.txt the instant a camp-out
// completes and the character actually drops:
//
//	2026-08-01 19:58:32	*** EXITING: I have completed camping.
//	2026-08-01 19:58:32	Networking: connection terminated [...]
//
// This is a definitive statement of the event, unlike the eqlog side, where the
// only evidence is a countdown followed by a silence long enough to be sure no
// more lines are coming — a window that unrelated periodic messages ("You are
// low on food." on its own cycle) can land inside. Watching for this instead
// removes the timing guesswork entirely.
const dbgCampMarker = "EXITING: I have completed camping"

// isDbgCampLine reports whether a dbg.txt line is a completed camp-out.
func isDbgCampLine(line string) bool {
	return strings.Contains(line, dbgCampMarker)
}

// findDbgLog resolves EQ's shared debug log (Logs\dbg.txt), preferring the most
// recently modified copy across the real Logs folder and the VirtualStore
// redirect (Program Files installs without admin write to the latter). Returns
// "" until the file exists — EQ creates it when it runs.
func findDbgLog(installDir string) string {
	best := ""
	var bestMod time.Time
	for _, logsDir := range logsDirCandidates(installDir) {
		p := filepath.Join(logsDir, "dbg.txt")
		if info, err := os.Stat(p); err == nil && (best == "" || info.ModTime().After(bestMod)) {
			best = p
			bestMod = info.ModTime()
		}
	}
	return best
}

// tailDbgLog watches Logs\dbg.txt and hides the timer overlays the instant a
// /q quit line appears. dbg.txt is a single shared file (not per character),
// appended across the whole EQ session, so we tail from the end and react only
// to new lines. Overlays are un-hidden automatically on the next eqlog line
// when the player logs back in (same auto-hide latch the camp-out path uses),
// so this only ever needs to set the hidden state — never clear it.
func tailDbgLog(installDir string, done <-chan struct{}) {
	path := findDbgLog(installDir)
	f, offset := openFromEnd(path)

	// Re-resolve periodically: dbg.txt may not exist at startup (EQ not yet
	// running) and the active copy can move between the real dir and VirtualStore.
	resolveTick := time.NewTicker(10 * time.Second)
	pollTick := time.NewTicker(1 * time.Second)
	defer resolveTick.Stop()
	defer pollTick.Stop()

	var partial string

	closeFile := func() {
		if f != nil {
			f.Close()
			f = nil
		}
	}

	for {
		select {
		case <-done:
			closeFile()
			return

		case <-resolveTick.C:
			if np := findDbgLog(installDir); np != "" && np != path {
				closeFile()
				path = np
				f, offset = openFromEnd(path)
				partial = ""
			}

		case <-pollTick.C:
			if path == "" {
				path = findDbgLog(installDir)
			}
			if f == nil {
				f, offset = openFromEnd(path)
				continue
			}

			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			newSize := info.Size()
			if newSize < offset {
				// Truncated/rotated (a fresh EQ launch can reset it) — resume from
				// the new end so we don't replay the whole file.
				closeFile()
				f, offset = openFromEnd(path)
				partial = ""
				continue
			}
			if newSize == offset {
				continue
			}

			buf := make([]byte, newSize-offset)
			n, err := f.ReadAt(buf, offset)
			if err != nil && err != io.EOF {
				continue
			}
			offset += int64(n)

			text := partial + string(buf[:n])
			scanner := bufio.NewScanner(strings.NewReader(text))
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}
			if len(text) > 0 && text[len(text)-1] != '\n' && len(lines) > 0 {
				partial = lines[len(lines)-1]
				lines = lines[:len(lines)-1]
			} else {
				partial = ""
			}

			for _, line := range lines {
				// Any exit counts, whichever client wrote it: dbg.txt is shared,
				// and a boxer's is indistinguishable from the tailed character's.
				// Freeze every category configured to auto-pause now, instead of
				// letting those bars burn until the 5-minute idle fallback (which
				// still covers crashes, where EQ writes no marker at all).
				switch {
				case isDbgQuitLine(line):
					QuitTriggerTimers()
					addStatus("Detected quit/exit — pausing timers and hiding overlays.")
				case isDbgCampLine(line):
					CampTriggerTimers()
					addStatus("Detected completed camp — pausing timers and hiding overlays.")
				default:
					continue
				}
				setPopoutsAutoHidden(true)
				// Leaving the world counts as leaving the zone for quest waypoints.
				go questMarkersCamp()
			}
		}
	}
}

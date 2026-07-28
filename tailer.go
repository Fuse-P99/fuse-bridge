package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tailLogFile reads new lines from the active log file and sends them to out.
// Polls every 100ms for new content and checks every 10s for log file switches.
// Runs until done is closed.
//
// The 100ms poll is deliberately tight: CH-chain clerics cue off a trigger bar
// appearing, and a cast a second late can drop the tank. An idle poll is a single
// os.Stat (10/sec) and reads nothing when the size is unchanged, so the cost is
// negligible. A partially-written trailing line is safe at any poll rate — it's
// held in `partial` until its newline arrives.
func tailLogFile(installDir, initialPath string, out chan<- string, done <-chan struct{}) {
	path := initialPath
	f, offset := openFromEnd(path)
	if f != nil {
		notifyLogFile(path)
		defer f.Close()
	}

	staleTick := time.NewTicker(10 * time.Second)
	pollTick := time.NewTicker(100 * time.Millisecond)
	defer staleTick.Stop()
	defer pollTick.Stop()

	var partial string

	for {
		select {
		case <-done:
			return

		case <-staleTick.C:
			newPath := checkForLogFileChange(installDir, path)
			if newPath != "" {
				if f != nil {
					f.Close()
				}
				path = newPath
				f, offset = openFromLogin(path)
				partial = ""
				if f != nil {
					notifyLogFile(path)
				}
			}

		case <-pollTick.C:
			if f == nil {
				f, offset = openFromEnd(path)
				if f != nil {
					notifyLogFile(path)
				}
				continue
			}

			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			newSize := info.Size()
			if newSize < offset {
				// Log shrank — players truncate/trim logs all the time. Resume
				// from the new END: reopening at byte 0 replayed the entire
				// remaining file, flooding the relay with old guild chat.
				f.Close()
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

			now := time.Now()
			staleReopen := false
			for _, line := range lines {
				if line == "" {
					continue
				}
				// Stale-line guard: a live EQ log line's timestamp is within
				// seconds of local time. A line more than 2h off (either
				// direction — 2h leaves slack for DST leaps and clock skew)
				// means we're reading archived/rotated content (e.g. another
				// program rolled the log and our cursor landed in old data).
				// Drop it and resync from the end so bad data never leaves the
				// client — a stronger, source-side version of the server's 24h
				// replay guard.
				if lt := logLineTime(line); !lt.IsZero() {
					diff := now.Sub(lt)
					if diff < 0 {
						diff = -diff
					}
					if diff > 2*time.Hour {
						staleReopen = true
						break
					}
				}
				select {
				case out <- line:
				case <-done:
					return
				}
			}

			if staleReopen {
				addStatus("Log timestamp far from system time — reopening log to resync (likely archived/rotated).")
				f.Close()
				f, offset = openFromEnd(path)
				partial = ""
			}
		}
	}
}

// logLineTime parses the "[Day Mon DD HH:MM:SS YYYY]" timestamp prefix of an EQ
// log line, in local time (EQ writes local time). Returns the zero time when
// the prefix is absent or unparseable, so such lines are never treated as stale.
func logLineTime(line string) time.Time {
	if len(line) < 26 || line[0] != '[' {
		return time.Time{}
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return time.Time{}
	}
	stamp := line[1:end]
	for _, layout := range []string{"Mon Jan 02 15:04:05 2006", "Mon Jan _2 15:04:05 2006"} {
		if t, err := time.ParseInLocation(layout, stamp, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

var currentCharName string // e.g. "Dustin" — extracted from eqlog_Dustin_server.txt

func notifyLogFile(path string) {
	base := filepath.Base(path)
	setLogFile(base)
	SetTrayStatus("Relay active — " + base)
	addStatus("Following log: %s", base)
	newName := charNameFromLog(base)
	// Character swap → discard the previous toon's position so the map doesn't
	// carry a stale dot into the new character.
	changed := newName != currentCharName
	if changed {
		ClearPosition()
		// The old toon left the world: pause the timers in their auto-pause
		// categories (resumed on their next login) and clear the rest. The new
		// toon's paused timers resume when their first log line is processed.
		if currentCharName != "" {
			PauseTriggerTimers(currentCharName, "character swap")
		}
	}
	currentCharName = newName
	// Trigger enablement and {C} patterns are per-character — rebuild the
	// active set for the new toon (also covers the initial log attach).
	// Done synchronously so trigActive/trigActiveChar are current before the
	// tailer feeds this toon's lines to ProcessTriggerLine: an async rebuild
	// let a recast fire against the OLD set and duplicate a paused timer that
	// hadn't been resumed yet (compiling the set is cheap — a few ms).
	if changed || newName != "" {
		RebuildTriggerActivation()
	}
	// Timer overlays are per character: swap in this toon's saved layout (the map
	// overlay is app-wide and is left alone).
	if newName != "" {
		ApplyPopoutsForCharacter(newName)
	}
}

// charNameFromLog extracts the character name from a filename like
// eqlog_Charactername_Servername.txt.
func charNameFromLog(base string) string {
	// strip "eqlog_" prefix and ".txt" suffix, then take the first segment
	s := strings.TrimPrefix(base, "eqlog_")
	s = strings.TrimSuffix(s, ".txt")
	parts := strings.SplitN(s, "_", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// openFromLogin opens the file and seeks to the start of the most recent
// "Welcome to EverQuest!" line so that login-time lines (zone entry, etc.)
// are captured when switching characters. Falls back to end-of-file if the
// marker is not found in the last 256 KB.
func openFromLogin(path string) (*os.File, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, 0
	}

	const lookback = 256 * 1024
	start := size - lookback
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return f, size // fall back to end
	}

	const marker = "Welcome to EverQuest!"
	idx := strings.LastIndex(string(buf), marker)
	if idx < 0 {
		return f, size // marker not found — fall back to end
	}

	// Rewind to the start of the line containing the marker.
	lineStart := strings.LastIndex(string(buf[:idx]), "\n") + 1
	return f, start + int64(lineStart)
}

func openFromEnd(path string) (*os.File, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, 0
	}
	return f, offset
}

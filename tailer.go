package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

	// Where we left each file we've tailed this session. A boxing session (two
	// EQ clients, two live logs) flips the active log back and forth every few
	// minutes — whichever was written last wins. Resuming from the remembered
	// offset means a revisit never re-processes lines it already handled (the
	// old openFromLogin replay re-fired every trigger since that log's login,
	// endlessly) and never misses the lines written while we were away.
	// Each checkpoint carries a content fingerprint: an offset is only a byte
	// number, and the file behind it can be archived away (Manage Logs moves
	// oversized logs aside and EQ recreates the name) or rewritten by a
	// third-party trimmer while we're following the other log.
	checkpoints := map[string]tailCheckpoint{}

	staleTick := time.NewTicker(10 * time.Second)
	// 50ms: an idle tick is one cached os.Stat (microseconds), so the rate is
	// effectively free — and it halves the worst-case log→screen latency for
	// time-critical displays like the CH chain cast bars.
	pollTick := time.NewTicker(50 * time.Millisecond)
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
					// Remember the resume point MINUS any half-written line we
					// were holding (re-reading it from its start reassembles it
					// whole on the way back — it was never emitted), plus the
					// fingerprint that makes it trustworthy later.
					if cp, ok := takeCheckpoint(f, offset-int64(len(partial))); ok {
						checkpoints[path] = cp
					} else {
						delete(checkpoints, path)
					}
					f.Close()
				}
				path = newPath
				// The left-world signal belonged to the log we just left.
				resetTailLeftSignal()
				// A checkpoint is only a resume point while it's FRESH. Under
				// session locking the other log can be ignored for hours, and
				// resuming from an hours-old offset would replay everything
				// that character did since — through triggers and the relay
				// (the 2h stale-line guard below wouldn't catch most of it).
				// An aged checkpoint is discarded; openFromLogin's own 5-min
				// login guard then lands at EOF.
				if cp, seen := checkpoints[path]; seen && time.Since(cp.at) <= tailCheckpointMax {
					f, offset = openAtCheckpoint(path, cp)
				} else {
					delete(checkpoints, path)
					f, offset = openFromLogin(path)
				}
				partial = ""
				if f != nil {
					notifyLogFile(path)
				}
			} else if f != nil {
				// Same path — but still the same FILE? Manage Logs archives by
				// renaming a quiet oversized log aside (EQ recreates the name on
				// the next login), and third-party rotators do the same swap.
				// Our handle would keep watching the moved file while
				// os.Stat(path) reports the new one; the poll loop's shrink
				// guard only heals that once the new file is smaller than our
				// offset. Compare identities and re-attach outright instead.
				if byName, err := os.Stat(path); err == nil {
					if byHandle, herr := f.Stat(); herr == nil && !os.SameFile(byHandle, byName) {
						addStatus("Log file was replaced on disk (archived/rotated) — re-attaching.")
						f.Close()
						delete(checkpoints, path)
						f, offset = openFromLogin(path)
						partial = ""
					}
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

// currentCharNameShared mirrors currentCharName for readers on OTHER
// goroutines (the dbg.txt tail's crash-marker scoping). The bare global stays
// for the tailer/trigger paths that already serialize with the writer
// (notifyLogFile is the only writer).
var currentCharNameShared atomic.Value // string

// ── left-world signal (feeds finder.go's tailSessionEnded) ──────────────────
//
// The lock on the tailed log releases when its character leaves the game.
// Farewell evidence comes from two places: the tailed log's own camp
// countdown lines (per-character, unambiguous) and dbg.txt's quit/exit/camp/
// crash markers (immediate, but dbg.txt is SHARED across boxed clients — a
// marker can belong to the other box). Each signal therefore carries a fuse
// of required log silence: if the tailed log keeps writing past the fuse, the
// signal wasn't ours and ProcessTriggerLine disarms it.

const (
	// tailSignalGrace protects a young signal from being disarmed by its own
	// neighbors: camp countdown lines repeat every ~5s, and a quit's last few
	// log lines can land just after the dbg marker.
	tailSignalGrace = 10 * time.Second
	// tailFuseScoped is the fuse for evidence attributable to the tailed
	// character: the log's own camp countdown, a crash naming them.
	tailFuseScoped = 60 * time.Second
	// tailFuseShared is the fuse for dbg.txt's unattributed quit/exit/camp
	// markers, which any boxed client can have written. The extra silence
	// keeps the OTHER box's quit from releasing our lock during a
	// coincidental quiet minute.
	tailFuseShared = 2 * time.Minute
)

var (
	tailSignalMu   sync.Mutex
	tailSignalAt   time.Time
	tailSignalFuse time.Duration
)

// noteTailLeftWorld arms (or re-arms) the left-world signal. scoped says the
// evidence names the tailed character; unscoped evidence gets the longer fuse.
// tailSignalMu is a leaf lock: holders never take another lock, so callers may
// hold trigStateMu (the campout hook does).
func noteTailLeftWorld(scoped bool) {
	fuse := tailFuseShared
	if scoped {
		fuse = tailFuseScoped
	}
	tailSignalMu.Lock()
	tailSignalAt = time.Now()
	tailSignalFuse = fuse
	tailSignalMu.Unlock()
}

// noteTailLineActivity disarms the signal when the tailed log meaningfully
// keeps living past it — the character aborted the camp, or the dbg marker
// belonged to another box. Young signals survive their own stragglers.
func noteTailLineActivity() {
	tailSignalMu.Lock()
	if !tailSignalAt.IsZero() && time.Since(tailSignalAt) > tailSignalGrace {
		tailSignalAt = time.Time{}
	}
	tailSignalMu.Unlock()
}

// tailLeftSignal returns the armed signal, if any.
func tailLeftSignal() (at time.Time, fuse time.Duration, armed bool) {
	tailSignalMu.Lock()
	defer tailSignalMu.Unlock()
	return tailSignalAt, tailSignalFuse, !tailSignalAt.IsZero()
}

// resetTailLeftSignal clears the signal — it belonged to the log we just left.
func resetTailLeftSignal() {
	tailSignalMu.Lock()
	tailSignalAt = time.Time{}
	tailSignalMu.Unlock()
}

// currentCharKey is the tailed character's per-(character, world) STORAGE key
// (see storeKeyFor): the bare lowercased name on Blue, "name@token" elsewhere.
// EQ names are only unique per server — a Green namesake must not read or
// write the Blue character's per-char state. Every per-character store keys on
// this; currentCharName stays the bare name for log matching, display, and
// server payloads.
var currentCharKey string

func notifyLogFile(path string) {
	base := filepath.Base(path)
	setLogFile(base)
	SetTrayStatus("Relay active — " + base)
	addStatus("Following log: %s", base)
	// Record which world this log belongs to BEFORE anything downstream can
	// forward: the whole guardrail (onHomeServer/serverForwardOK) keys on this.
	prevHome := onHomeServer()
	setCurrentServerToken(serverTokenFromLog(base))
	if lbl := currentServerLabel(); lbl != "" {
		if onHomeServer() {
			addStatus("Server: %s", lbl)
		} else {
			addStatus("Server: %s — reduced mode (nothing sent to the server; linked members still see the Fuse package and Blue raid boards)", lbl)
		}
	}
	// Arriving on Blue (from startup or from another world): (re)announce the
	// Blue toon inventory so member↔toon association stays current. Identify is
	// skipped entirely while off-home, so this is what heals it on the way back.
	if onHomeServer() && !prevHome {
		go identifyClient(GetSettings().EQDirectory)
	}
	newName := charNameFromLog(base)
	newKey := storeKeyFor(newName, getCurrentServerToken())
	// Character swap → discard the previous toon's position so the map doesn't
	// carry a stale dot into the new character. Compared by STORAGE KEY, not
	// name: Franko(Blue) → Franko(Green) is a swap between two characters even
	// though the bare name never changed.
	changed := newKey != currentCharKey
	if changed {
		ClearPosition()
		// The old toon left the world: pause the timers in their auto-pause
		// categories (resumed on their next login) and clear the rest. The new
		// toon's paused timers resume when their first log line is processed.
		// The stash key is the OLD currentCharKey — built when THEIR world's
		// token was current — never recomputed now that the token has flipped.
		if currentCharName != "" {
			PauseTriggerTimers(currentCharKey, "character swap")
		}
	}
	currentCharName = newName
	currentCharNameShared.Store(newName)
	currentCharKey = newKey
	// First sighting of an off-home character: adopt their pre-qualified-key
	// buckets if the bare name can't belong to a Blue character (see
	// adoptOffHomeCharState). Before RebuildTriggerActivation so the adopted
	// toggle set is what activation compiles against.
	if changed && newName != "" {
		adoptOffHomeCharState(newName, newKey)
	}
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
		ApplyPopoutsForCharacter(newKey)
		// Quest tracking follows the character too: pull their server state if
		// unseen this session, rescan their inventory, check their epic.
		questTrackCharSwap(newName, newKey)
	}
}

// adoptOffHomeCharState runs on the first swap onto an off-home character this
// session. Per-character state used to be keyed by bare name; if this
// character's qualified buckets don't exist yet AND no Blue log claims the
// bare name (so the bare buckets can only have been created by this very
// character playing off-home under the old keying), COPY them over so nothing
// they configured is lost. When a Blue log DOES exist the bare buckets belong
// to the Blue character and the off-home namesake starts fresh — which is the
// whole point of the qualified key.
func adoptOffHomeCharState(name, key string) {
	if _, token := splitStoreKey(key); token == "" {
		return // home world — bare keys, nothing to adopt
	}
	if homeLogExistsFor(GetSettings().EQDirectory, name) {
		return
	}
	bare := strings.ToLower(strings.TrimSpace(name))
	adopted := adoptTrigTogglesBucket(bare, key)
	adopted = adoptPopoutCharBucket(bare, key) || adopted
	adopted = adoptQuestToonBucket(bare, key) || adopted
	if adopted {
		addStatus("Carried %s's existing settings over to %s (per-world storage).",
			name, charKeyDisplay(key))
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

// tailCheckpoint is where we left a file we may come back to: the resume
// offset plus the bytes immediately BEFORE it, which prove on return that the
// file still holds the content we read — not an archived-and-recreated or
// trimmed-and-rewritten one where that byte offset lands in different lines.
// `at` is when the checkpoint was taken: content integrity is the fingerprint's
// job, but whether resuming is a good IDEA is a question of age (see the
// tailCheckpointMax gate at the switch site).
type tailCheckpoint struct {
	off int64
	sig []byte
	at  time.Time
}

// tailSigLen is the fingerprint size — a couple of log lines' worth, plenty to
// make an accidental match at the same offset in rewritten content implausible.
const tailSigLen = 256

// tailCheckpointMax bounds how old a checkpoint may be and still be resumed
// from. Session locking can ignore a log for hours; the short bound keeps the
// quick-detour case seamless (a Blue AH check mid-Green-session replays the
// few minutes it missed) while a long-ignored log re-attaches at the end.
const tailCheckpointMax = 10 * time.Minute

// takeCheckpoint captures the resume point from a still-open handle. A
// checkpoint without a readable fingerprint is worthless (we could never trust
// it), so a failed read stores nothing and the next visit re-attaches fresh.
func takeCheckpoint(f *os.File, off int64) (tailCheckpoint, bool) {
	if off < 0 {
		return tailCheckpoint{}, false
	}
	cp := tailCheckpoint{off: off, at: time.Now()}
	sigLen := int64(tailSigLen)
	if sigLen > off {
		sigLen = off // near the start of the file: fingerprint what exists
	}
	if sigLen > 0 {
		sig := make([]byte, sigLen)
		if _, err := f.ReadAt(sig, off-sigLen); err != nil {
			return tailCheckpoint{}, false
		}
		cp.sig = sig
	}
	return cp, true
}

// openAtCheckpoint reopens a previously-tailed file at the offset we left it —
// after verifying the fingerprint. Any mismatch (file shrank, or the bytes at
// the resume point changed) means an archiver or trimmer touched it while we
// were following the other log; resuming blind would replay old lines and
// re-fire their triggers. Such a file is treated as brand new: openFromLogin
// decides (replay a fresh login's lines, otherwise start at the end).
func openAtCheckpoint(path string, cp tailCheckpoint) (*os.File, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, 0
	}
	if cp.off > size {
		f.Close()
		return openFromLogin(path)
	}
	if len(cp.sig) > 0 {
		buf := make([]byte, len(cp.sig))
		if _, err := f.ReadAt(buf, cp.off-int64(len(cp.sig))); err != nil || !bytes.Equal(buf, cp.sig) {
			f.Close()
			return openFromLogin(path)
		}
	}
	return f, cp.off
}

// openFromLogin opens the file and seeks to the start of the most recent
// "Welcome to EverQuest!" line so that login-time lines (zone entry, etc.)
// are captured when switching characters. Falls back to end-of-file if the
// marker is not found in the last 256 KB — or if the login is not RECENT:
// on a genuine character swap the relay notices the new log within ~20s of
// "Welcome to EverQuest!", so a marker minutes old means this log belongs to
// a session that has been running for a while (a boxed second client). Replaying
// its whole history would re-fire every trigger since login — the invite, the
// tells — every time the active log flips to it.
const loginReplayMax = 5 * time.Minute

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

	// Only replay when that login just happened (see loginReplayMax above).
	markerLine := string(buf[lineStart:])
	if nl := strings.IndexByte(markerLine, '\n'); nl >= 0 {
		markerLine = markerLine[:nl]
	}
	if lt := logLineTime(markerLine); lt.IsZero() || time.Since(lt) > loginReplayMax {
		return f, size
	}

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

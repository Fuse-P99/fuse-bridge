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
// Polls every 500ms for new content and checks every 10s for log file switches.
// Runs until done is closed.
func tailLogFile(installDir, initialPath string, out chan<- string, done <-chan struct{}) {
	path := initialPath
	f, offset := openFromEnd(path)
	if f != nil {
		notifyLogFile(path)
		defer f.Close()
	}

	staleTick := time.NewTicker(10 * time.Second)
	pollTick := time.NewTicker(500 * time.Millisecond)
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
				// Log was rotated — reopen from start
				f.Close()
				f, err = os.Open(path)
				if err != nil {
					f = nil
					continue
				}
				offset = 0
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
				if line == "" {
					continue
				}
				select {
				case out <- line:
				case <-done:
					return
				}
			}
		}
	}
}

var currentCharName string // e.g. "Dustin" — extracted from eqlog_Dustin_server.txt

func notifyLogFile(path string) {
	base := filepath.Base(path)
	setLogFile(base)
	SetTrayStatus("Relay active — " + base)
	addStatus("Following log: %s", base)
	currentCharName = charNameFromLog(base)
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

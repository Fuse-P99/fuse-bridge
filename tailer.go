package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tailLogFile reads new lines from the active log file and sends them to out.
// It polls every 500ms for new content and checks every 10s for log file changes.
// Runs until the done channel is closed.
func tailLogFile(installDir, initialPath string, out chan<- string, done <-chan struct{}, statusFn func(string)) {
	path := initialPath
	f, offset := openFromEnd(path)
	if f != nil {
		statusFn("Following log: " + filepath.Base(path))
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
				f, offset = openFromEnd(path)
				partial = ""
				if f != nil {
					statusFn("Following log: " + filepath.Base(path))
				}
			}

		case <-pollTick.C:
			if f == nil {
				// Try to open again if file appeared
				f, offset = openFromEnd(path)
				if f != nil {
					statusFn("Following log: " + filepath.Base(path))
				}
				continue
			}

			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			newSize := info.Size()
			if newSize < offset {
				// Log was rotated (truncated or replaced); reopen from start
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

			// Split on newlines, preserving partial last line
			text := partial + string(buf[:n])
			scanner := bufio.NewScanner(strings.NewReader(text))
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}

			// If text doesn't end with newline, the last piece is incomplete
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

func openFromEnd(path string) (*os.File, int64) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("Cannot open log file %s: %v\n", path, err)
		return nil, 0
	}
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, 0
	}
	return f, offset
}

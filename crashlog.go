package main

// Where errors go when there is no console to see them.
//
// A windowed (-H windowsgui) process has no standard error stream anyone can
// read. The Go runtime writes a panic's stack trace there, so a client that
// died of a panic left the launch log ending mid-sentence and nothing in Event
// Viewer: a Go exit is not a Windows crash, so no Event 1000 is ever written.
// Wails has the same blind spot — it discards its own log unless given a
// logger, and its fatal path exits the process right after an error nobody was
// listening to. A member's client died at every launch for a week (2026-10-07)
// with no trail anywhere; this file is the trail.

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// crashLogPath sits beside the launch log (FuseBridge.log), so one folder
// holds both.
var crashLogPath = filepath.Join(os.TempDir(), "FuseBridge-crash.log")

// crashLogMax caps the file: past this it is rolled to .1 at the next launch,
// so a client that panics on every start for a month does not fill the disk.
const crashLogMax = 2 << 20

// crashLogFile stays open for the life of the process. The runtime writes to
// the handle, and closing it would turn panics back into silence.
var crashLogFile *os.File

// initCrashLog points the process's standard error handle at the crash log.
// The Go runtime looks that handle up on every write (GetStdHandle, not a
// handle cached at startup), so redirecting it here — the first thing main
// does — routes every later panic, fatal runtime error, and stray stderr write
// into the file. One header line is written per launch so a trace can be
// matched to the launch-log entry for the same process; a file holding only
// headers means nothing has gone wrong.
func initCrashLog() {
	if fi, err := os.Stat(crashLogPath); err == nil && fi.Size() > crashLogMax {
		os.Rename(crashLogPath, crashLogPath+".1") // best effort; the open below still works
	}
	f, err := os.OpenFile(crashLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	crashLogFile = f
	fmt.Fprintf(f, "=== FuseBridge %s pid %d launched %s ===\n",
		clientVersion, os.Getpid(), time.Now().Format("2006-01-02 15:04:05"))
	os.Stderr = f
	windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
}

// wailsLogWriter feeds Wails' structured log into the launch log, one line per
// record. The handler is told to drop its own time attribute (writeLog stamps).
type wailsLogWriter struct{}

func (wailsLogWriter) Write(p []byte) (int, error) {
	if s := strings.TrimSpace(string(p)); s != "" {
		writeLog("wails: " + s)
	}
	return len(p), nil
}

// wailsLogger is the logger handed to Wails. Warnings and errors only — the
// window layer's info chatter during overlay creation would bury the lines
// that matter.
func wailsLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(wailsLogWriter{}, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}

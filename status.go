package main

import (
	"fmt"
	"sync"
	"time"
)

var appState struct {
	mu        sync.RWMutex
	lines     []string
	eqRunning bool
	logFile   string
	connected bool
}

func addStatus(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("15:04:05")
	appState.mu.Lock()
	appState.lines = append(appState.lines, "["+ts+"] "+msg)
	if len(appState.lines) > 100 {
		appState.lines = appState.lines[1:]
	}
	appState.mu.Unlock()
}

func setLogFile(base string) {
	appState.mu.Lock()
	appState.logFile = base
	appState.eqRunning = base != ""
	appState.mu.Unlock()
}

func setConnected(connected bool) {
	appState.mu.Lock()
	appState.connected = connected
	appState.mu.Unlock()
}

func getStatusSnapshot() (eqRunning bool, logFile string, connected bool, lines []string) {
	appState.mu.RLock()
	defer appState.mu.RUnlock()
	cp := make([]string, len(appState.lines))
	copy(cp, appState.lines)
	return appState.eqRunning, appState.logFile, appState.connected, cp
}

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type versionResponse struct {
	Version string `json:"version"`
}

// lastLogActivity is updated by the filter goroutine each time a line arrives
// from the EQ log. Used to determine whether the game is actively being played.
var lastLogActivity time.Time

// logIsStale returns true when no EQ log line has been seen for at least an
// hour, indicating the game is not being played and it is safe to restart.
func logIsStale() bool {
	// Zero means no activity since the relay started — treat as stale.
	return lastLogActivity.IsZero() || time.Since(lastLogActivity) >= 1*time.Hour
}

// startUpdateChecker checks for a new client binary on startup and then every
// 6 hours, but only when EQ logs have been quiet for at least an hour.
func startUpdateChecker() {
	go func() {
		checkForUpdate()
		for range time.Tick(6 * time.Hour) {
			checkForUpdate()
		}
	}()
}

// updateStampPath returns the path of the file used to track the last update attempt.
func updateStampPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "FuseBridge-update.stamp")
}

// recentUpdateAttempt returns true if an update was attempted in the last 30 minutes.
// This prevents a restart loop when the server is serving an exe with a stale version.
func recentUpdateAttempt() bool {
	p := updateStampPath()
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < 30*time.Minute
}

func touchUpdateStamp() {
	p := updateStampPath()
	if p == "" {
		return
	}
	os.WriteFile(p, nil, 0644)
}

func checkForUpdate() {
	if !logIsStale() {
		return
	}
	if recentUpdateAttempt() {
		return
	}
	base := strings.TrimSuffix(serverURL, "/submit")
	resp, err := http.Get(base + "/version")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var vr versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return
	}

	if vr.Version == "" || vr.Version == clientVersion {
		return
	}
	if !versionGreaterThan(vr.Version, clientVersion) {
		return // server version is not newer; don't downgrade
	}

	addStatus("Update available (%s → %s), downloading...", clientVersion, vr.Version)
	applyUpdate(base)
}

// versionGreaterThan returns true when a is strictly newer than b.
// Versions are expected in "major.minor.patch" form.
func versionGreaterThan(a, b string) bool {
	parse := func(v string) [3]int {
		var parts [3]int
		segs := strings.SplitN(v, ".", 3)
		for i, s := range segs {
			if i >= 3 {
				break
			}
			parts[i], _ = strconv.Atoi(s)
		}
		return parts
	}
	av, bv := parse(a), parse(b)
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}

func applyUpdate(baseURL string) {
	touchUpdateStamp()
	exePath, err := os.Executable()
	if err != nil {
		addStatus("Update failed: cannot find executable path: %v", err)
		return
	}
	exeDir := filepath.Dir(exePath)
	newExePath := filepath.Join(exeDir, "FuseBridge-new.exe")

	req, err := http.NewRequest(http.MethodGet, baseURL+"/client", nil)
	if err != nil {
		addStatus("Update failed: %v", err)
		return
	}
	req.Header.Set("Authorization", authHeader())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		addStatus("Update download failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		addStatus("Update download failed: server returned %d", resp.StatusCode)
		return
	}

	f, err := os.Create(newExePath)
	if err != nil {
		addStatus("Update failed: cannot create temp file: %v", err)
		return
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(newExePath)
		addStatus("Update failed: download interrupted: %v", err)
		return
	}
	f.Close()

	// Launch a hidden PowerShell process that waits for this process to exit,
	// swaps the binary, and relaunches it. PowerShell with -WindowStyle Hidden
	// plus CREATE_NO_WINDOW on the spawning side means no console ever appears.
	script := fmt.Sprintf(
		"Start-Sleep -Seconds 3; "+
			"Move-Item -Force '%s' '%s'; "+
			"Start-Process '%s'",
		newExePath, exePath, exePath,
	)
	if err := noWindowCmd("powershell",
		"-WindowStyle", "Hidden",
		"-NoProfile", "-NonInteractive",
		"-Command", script,
	).Start(); err != nil {
		os.Remove(newExePath)
		addStatus("Update failed: cannot launch update script: %v", err)
		return
	}

	addStatus("Restarting for update...")
	os.Exit(0)
}

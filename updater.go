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

// startUpdateChecker checks for a new client binary every 6 hours, but only when
// EQ logs have been quiet for at least an hour. The initial startup check is
// handled separately (see main.go) so the upgrade screen can be shown first.
func startUpdateChecker() {
	go func() {
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

// availableUpdate reports whether the server offers a strictly newer client,
// regardless of whether it's safe to auto-restart right now. Used by the
// manual Update button (user-initiated) and by updateInfo below.
func availableUpdate() (string, string, bool) {
	base := strings.TrimSuffix(serverURL, "/submit")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(base + "/version")
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", false
	}
	var vr versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return "", "", false
	}
	if vr.Version == "" || vr.Version == clientVersion {
		return "", "", false
	}
	if !versionGreaterThan(vr.Version, clientVersion) {
		return "", "", false // server version is not newer; don't downgrade
	}
	return base, vr.Version, true
}

// updateInfo reports whether a newer client is available and safe to install
// now, returning (baseURL, newVersion, true) when so.
func updateInfo() (string, string, bool) {
	if !logIsStale() {
		return "", "", false
	}
	if recentUpdateAttempt() {
		return "", "", false
	}
	return availableUpdate()
}

func checkForUpdate() {
	base, newVer, ok := updateInfo()
	if !ok {
		return
	}
	addStatus("Update available (%s → %s), downloading...", clientVersion, newVer)
	if err := applyUpdate(base); err != nil {
		addStatus("Update failed: %v", err)
		writeLog("periodic update failed: " + err.Error())
	}
}

// cleanupFailedUpdate detects a leftover FuseBridge-new.exe from a previous
// attempt whose swap never completed (the exe stayed locked, so the swap script
// relaunched the old build). Removes it and surfaces the fact so "still on the
// old version" reports are diagnosable; the retry happens through the normal
// startup/periodic checks or the manual Update button. The age guard avoids
// racing a swap script that is still inside its retry window.
func cleanupFailedUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	p := filepath.Join(filepath.Dir(exe), "FuseBridge-new.exe")
	if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > 5*time.Minute {
		writeLog("leftover FuseBridge-new.exe — previous update swap did not complete")
		addStatus("The previous update could not replace the app file; it will be retried automatically.")
		os.Remove(p)
	}
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

// applyUpdate downloads the new binary and hands off to the swap script. On
// success it never returns (the process exits for the restart); any error is
// returned so the caller can back out of the upgrade screen and retry later.
func applyUpdate(baseURL string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	newExePath := filepath.Join(exeDir, "FuseBridge-new.exe")

	req, err := http.NewRequest(http.MethodGet, baseURL+"/client", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader())

	// The binary is ~30MB; on a slow connection a full download can far exceed
	// the old 2-minute cap. This timeout is a stall guard, not a speed test.
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: server returned %d", resp.StatusCode)
	}

	f, err := os.Create(newExePath)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	discard := func() {
		f.Close()
		os.Remove(newExePath)
	}
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		discard()
		return fmt.Errorf("download interrupted: %w", err)
	}
	// Never swap in a truncated or bogus file: the byte count must match the
	// server's Content-Length and be a plausible size for the client binary.
	if resp.ContentLength > 0 && n != resp.ContentLength {
		discard()
		return fmt.Errorf("download incomplete: got %d of %d bytes", n, resp.ContentLength)
	}
	if n < 1<<20 {
		discard()
		return fmt.Errorf("downloaded file too small to be the client (%d bytes)", n)
	}
	if err := f.Sync(); err != nil {
		discard()
		return fmt.Errorf("cannot flush download to disk: %w", err)
	}
	f.Close()

	// Launch a hidden PowerShell process that waits for THIS process to actually
	// exit (not a blind sleep), then retries the swap for up to 30s — antivirus
	// scans of the fresh exe and slow handle teardown routinely hold locks past
	// any fixed delay. Progress goes to FuseBridge-update.log next to the exe so
	// failed swaps are diagnosable, and the relaunch happens regardless of the
	// swap outcome so the user is never left with no app at all. PowerShell with
	// -WindowStyle Hidden plus CREATE_NO_WINDOW on the spawning side means no
	// console ever appears.
	updateLog := filepath.Join(exeDir, "FuseBridge-update.log")
	script := fmt.Sprintf(
		"('update started ' + (Get-Date)) | Out-File -FilePath '%s'; "+
			"Wait-Process -Id %d -Timeout 60 -ErrorAction SilentlyContinue; "+
			"$moved = $false; "+
			"foreach ($i in 1..30) { "+
			"try { Move-Item -Force -ErrorAction Stop '%s' '%s'; $moved = $true; break } "+
			"catch { Start-Sleep -Seconds 1 } "+
			"}; "+
			"('moved=' + $moved + ' ' + (Get-Date)) | Out-File -FilePath '%s' -Append; "+
			"Start-Process '%s'",
		updateLog, os.Getpid(), newExePath, exePath, updateLog, exePath,
	)
	if err := noWindowCmd("powershell",
		"-WindowStyle", "Hidden",
		"-NoProfile", "-NonInteractive",
		"-Command", script,
	).Start(); err != nil {
		os.Remove(newExePath)
		return fmt.Errorf("cannot launch update script: %w", err)
	}

	// Only a successful handoff marks an attempt: the stamp exists to break
	// restart loops when the server serves a stale-versioned exe, not to
	// suppress retries after a failed download.
	touchUpdateStamp()
	// Buffs (Self)/Disciplines timers normally persist on clean shutdown; do it
	// here too since os.Exit skips main's teardown.
	PersistTriggerTimersNow()
	addStatus("Restarting for update...")
	os.Exit(0)
	return nil
}

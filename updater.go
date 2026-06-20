package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type versionResponse struct {
	Version string `json:"version"`
}

// startUpdateChecker checks for a new client binary on startup and then hourly.
func startUpdateChecker() {
	go func() {
		checkForUpdate()
		for range time.Tick(1 * time.Hour) {
			checkForUpdate()
		}
	}()
}

func checkForUpdate() {
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

	addStatus("Update available (%s → %s), downloading...", clientVersion, vr.Version)
	applyUpdate(base)
}

func applyUpdate(baseURL string) {
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
	req.Header.Set("Authorization", "Bearer "+apiKey)

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

	// Write a batch script that waits for this process to exit, swaps the
	// binary, relaunches, then deletes itself.
	batchPath := filepath.Join(exeDir, "FuseBridge-update.bat")
	batch := fmt.Sprintf(
		"@echo off\r\n"+
			"ping -n 3 127.0.0.1 > nul\r\n"+
			"move /Y \"%s\" \"%s\"\r\n"+
			"start \"\" \"%s\"\r\n"+
			"del \"%%~f0\"\r\n",
		newExePath, exePath, exePath,
	)
	if err := os.WriteFile(batchPath, []byte(batch), 0600); err != nil {
		os.Remove(newExePath)
		addStatus("Update failed: cannot write update script: %v", err)
		return
	}

	addStatus("Restarting for update...")
	if err := exec.Command("cmd", "/C", "start", "", "/MIN", batchPath).Start(); err != nil {
		os.Remove(newExePath)
		os.Remove(batchPath)
		addStatus("Update failed: cannot launch update script: %v", err)
		return
	}

	os.Exit(0)
}

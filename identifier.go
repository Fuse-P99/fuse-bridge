package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type toonWeightPayload struct {
	Name   string `json:"name"`
	Weight int64  `json:"weight"`
}

type identifyPayload struct {
	Version string              `json:"version"`
	Toons   []toonWeightPayload `json:"toons"`
}

// gatherToonWeights scans the EQ Logs directory for eqlog_CHARNAME_SERVER.txt files
// and returns each character paired with its log file size as a play-time proxy.
func gatherToonWeights(eqDir string) []toonWeightPayload {
	entries, err := os.ReadDir(filepath.Join(eqDir, "Logs"))
	if err != nil {
		return nil
	}
	var weights []toonWeightPayload
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "eqlog_") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		// eqlog_CHARNAME_SERVERNAME.txt → CHARNAME is the first underscore-delimited segment
		inner := strings.TrimSuffix(strings.TrimPrefix(name, "eqlog_"), ".txt")
		parts := strings.SplitN(inner, "_", 2)
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		weights = append(weights, toonWeightPayload{Name: parts[0], Weight: info.Size()})
	}
	return weights
}

// identifyClient posts the local log-file inventory to the server so it can link
// this client's IP to a guild member. Called once on startup after EQ is found.
func identifyClient(eqDir string) {
	toons := gatherToonWeights(eqDir)
	body, err := json.Marshal(identifyPayload{Version: clientVersion, Toons: toons})
	if err != nil {
		return
	}

	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodPost, base+"/identify", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		addStatus("Identify error: %v", err)
		return
	}
	defer resp.Body.Close()
	addStatus("Identified to server (%d log files found)", len(toons))
}

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

type toonWeightPayload struct {
	Name   string `json:"name"`
	Weight int64  `json:"weight"`
	// LastPlayedMs is the log file's mtime — when THIS install last wrote a
	// line for the character. The cross-guild report shows it as the member's
	// own last play, which a /who sighting can't give on a shared account.
	LastPlayedMs int64 `json:"last_played_ms"`
}

type identifyPayload struct {
	Version string              `json:"version"`
	Toons   []toonWeightPayload `json:"toons"`
}

// gatherToonWeights scans the EQ Logs directory for eqlog_CHARNAME_SERVER.txt files
// and returns each character paired with its log file size as a play-time proxy.
func gatherToonWeights(eqDir string) []toonWeightPayload {
	var weights []toonWeightPayload
	seen := make(map[string]bool)
	for _, logsDir := range logsDirCandidates(eqDir) {
		entries, err := os.ReadDir(logsDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "eqlog_") || !strings.HasSuffix(name, ".txt") {
				continue
			}
			// Home world only: the server keys toon↔member on a bare character
			// name, so reporting a Green/Red/test alt here would collide it with
			// a same-named Blue toon. Off-world characters are never announced.
			if !strings.EqualFold(serverTokenFromLog(name), homeServerToken) {
				continue
			}
			// eqlog_CHARNAME_SERVERNAME.txt → CHARNAME is the first underscore-delimited segment
			inner := strings.TrimSuffix(strings.TrimPrefix(name, "eqlog_"), ".txt")
			parts := strings.SplitN(inner, "_", 2)
			if len(parts) < 1 || parts[0] == "" || seen[strings.ToLower(parts[0])] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			seen[strings.ToLower(parts[0])] = true
			weights = append(weights, toonWeightPayload{
				Name:         parts[0],
				Weight:       info.Size(),
				LastPlayedMs: info.ModTime().UnixMilli(),
			})
		}
	}
	return weights
}

// identifyClient posts the local log-file inventory to the server so it can link
// this client's IP to a guild member. Called on startup after EQ is found, then
// hourly (main.go) so the toon inventory and last-played times stay fresh on
// clients that run for days. Skipped while unlinked — the endpoint needs a
// per-client token, and linking re-announces identity itself (PollLinking
// calls this on success).
func identifyClient(eqDir string) {
	// Home world only. Off-home the client is serverless: identify announces
	// the member's toon inventory, which the guardrail keeps Blue-only. Blue
	// toon association is re-announced on the next arrival on Blue (notifyLogFile).
	if !serverForwardOK() {
		return
	}
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
	req.Header.Set("Authorization", authHeader())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		addStatus("Identify error: %v", err)
		return
	}
	defer resp.Body.Close()
	addStatus("Identified to server (%d log files found)", len(toons))
	// A successful authenticated round-trip IS a server connection — light the
	// General tab's indicator. Before this, only the log-line sender's first
	// successful submit flipped it, so a fresh install showed "Not connected"
	// after linking (identify, heartbeats, and data all working) until the
	// first forwarded log line happened to go out. Identify runs right after
	// the link code is redeemed (PollLinking), at startup, and hourly, so the
	// light now heals on its own. Failures stay the sender's call — a
	// transient identify error must not blank an otherwise live connection.
	if resp.StatusCode == http.StatusOK {
		setConnected(true)
		SetTrayConnected(true)
	}
}

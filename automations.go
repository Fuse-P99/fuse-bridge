package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AutomationSettings mirrors the server's per-member raid-log automations
// (General tab → Automations). RosteredToons feeds the main-character dropdown.
type AutomationSettings struct {
	AddTracking   bool     `json:"add_tracking"`
	SwapBot       bool     `json:"swap_bot"`
	AddMissed     bool     `json:"add_missed"`
	MainToon      string   `json:"main_toon"`
	RosteredToons []string `json:"rostered_toons"`
}

// fetchAutomations retrieves the linked member's automation settings.
func fetchAutomations() (AutomationSettings, error) {
	var out AutomationSettings
	base := strings.TrimSuffix(serverURL, "/submit")
	req, err := http.NewRequest(http.MethodGet, base+"/automations", nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

// saveAutomations stores the automation settings server-side. The server clears
// the main toon when both tracking/swap toggles are off and validates the main is
// rostered. addMissed is independent — it needs no main toon.
func saveAutomations(addTracking, swapBot, addMissed bool, mainToon string) error {
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(AutomationSettings{
		AddTracking: addTracking, SwapBot: swapBot, AddMissed: addMissed, MainToon: mainToon,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/automations", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

package main

// Magelo view support: batch item lookups against the server's eqitems table.
// The endpoint is officer-only server-side (a lookup queues wiki scrapes for
// unknown items), and the Characters tab hides the Magelo sub-tab from
// non-officers to match.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MageloItem mirrors the server's item record for the Magelo view.
type MageloItem struct {
	Name      string  `json:"name"`
	Link      string  `json:"link"`
	Icon      string  `json:"icon"`
	Magic     bool    `json:"magic"`
	Lore      bool    `json:"lore"`
	NoDrop    bool    `json:"nodrop"`
	NoRent    bool    `json:"norent"`
	Slot      string  `json:"slot"`
	Skill     string  `json:"skill"`
	Dmg       int     `json:"dmg"`
	Delay     int     `json:"delay"`
	Range     int     `json:"range"`
	AC        int     `json:"ac"`
	Str       int     `json:"str"`
	Sta       int     `json:"sta"`
	Dex       int     `json:"dex"`
	Int       int     `json:"int"`
	Wis       int     `json:"wis"`
	Cha       int     `json:"cha"`
	Agi       int     `json:"agi"`
	HP        int     `json:"hp"`
	Mana      int     `json:"mana"`
	SvFire    int     `json:"sv_fire"`
	SvCold    int     `json:"sv_cold"`
	SvDisease int     `json:"sv_disease"`
	SvPoison  int     `json:"sv_poison"`
	SvMagic   int     `json:"sv_magic"`
	Effect    string  `json:"effect"`
	Weight    float32 `json:"wt"`
	Size      string  `json:"size"`
	Classes   string  `json:"classes"`
	Races     string  `json:"races"`
	Capacity  int     `json:"capacity"`
	SizeCap   string  `json:"size_capacity"`
	WR        int     `json:"wr"`
	Charges   int     `json:"charges"`
	Era       string  `json:"era"`
}

// MageloLookup is what LookupItems returns to the frontend.
type MageloLookup struct {
	Items   map[string]MageloItem `json:"items"`    // lower(name) → item
	Missing []string              `json:"missing"`  // unknown names, queued for scraping
	IconURL string                `json:"icon_url"` // base URL; append the icon filename
}

// LookupItems resolves inventory item names against the server's item DB.
// Unknown items are queued server-side for a wiki scrape — a later reopen of
// the Magelo tab picks them up.
func (a *App) LookupItems(names []string) (MageloLookup, error) {
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(map[string]any{"names": names})
	req, err := http.NewRequest(http.MethodPost, base+"/items/lookup", bytes.NewReader(body))
	if err != nil {
		return MageloLookup{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return MageloLookup{}, fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return MageloLookup{}, fmt.Errorf("the Magelo view is available to officers only")
	case http.StatusUnauthorized:
		return MageloLookup{}, fmt.Errorf("link your Discord account first")
	default:
		return MageloLookup{}, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var out MageloLookup
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return MageloLookup{}, err
	}
	if out.Items == nil {
		out.Items = map[string]MageloItem{}
	}
	out.IconURL = base + "/itemicon?f="
	return out, nil
}

// mageloPost is the shared authenticated POST for the magelo endpoints,
// decoding into out when non-nil and mapping the officer/link errors.
func mageloPost(path string, payload any, out any) error {
	base := strings.TrimSuffix(serverURL, "/submit")
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return fmt.Errorf("officers only")
	case http.StatusUnauthorized:
		return fmt.Errorf("link your Discord account first")
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		if t := strings.TrimSpace(string(msg)); t != "" {
			return fmt.Errorf("%s", t)
		}
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// SaveMagelo snapshots a character's full inventory to the server as their
// "current" magelo (called when the Magelo tab opens; officer-gated
// server-side). Paired slots are numbered the way the view shows them.
func (a *App) SaveMagelo(charName string) error {
	items := readInventoryItems(charName, GetSettings().EQDirectory)
	if len(items) == 0 {
		return fmt.Errorf("no inventory file for %s", charName)
	}
	type slotReq struct {
		Slot  string `json:"slot"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	slots := make([]slotReq, 0, len(items))
	seen := map[string]int{}
	for _, it := range items {
		slot := it.Location
		switch slot {
		case "Ear", "Wrist":
			seen[slot]++
			n := seen[slot]
			if n > 2 {
				n = 2
			}
			slot = fmt.Sprintf("%s%d", slot, n)
		case "Fingers", "Finger":
			seen["Finger"]++
			n := seen["Finger"]
			if n > 2 {
				n = 2
			}
			slot = fmt.Sprintf("Finger%d", n)
		}
		slots = append(slots, slotReq{Slot: slot, Name: it.Name, Count: it.Count})
	}
	return mageloPost("/magelo/save", map[string]any{
		"toon": charName, "magelo": "current", "slots": slots,
	}, nil)
}

// PreviewItem scrapes a wiki item link server-side and returns the parsed
// record for the Add Item dialog — nothing is saved until CommitItem.
func (a *App) PreviewItem(link string) (MageloItem, error) {
	var out struct {
		Item MageloItem `json:"item"`
	}
	if err := mageloPost("/items/preview", map[string]any{"link": link}, &out); err != nil {
		return MageloItem{}, err
	}
	return out.Item, nil
}

// CommitItem saves an approved (possibly corrected) item record to eqitems.
func (a *App) CommitItem(item MageloItem) error {
	return mageloPost("/items/commit", map[string]any{"item": item}, nil)
}

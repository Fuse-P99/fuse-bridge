package main

// Magelo view support: batch item lookups against the server's eqitems table.
// Released to all users — every endpoint here needs a linked client, and the
// item/buff admin tools stay officer-only server-side.

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
	Atk       int     `json:"atk"`
	// Stub marks a placeholder — a row created by hand so a quest could
	// reference an item the wiki has no page for. Every stat above it is zero
	// and stays that way, so display it as a name rather than as an item whose
	// stats happen to be blank.
	Stub bool `json:"stub,omitempty"`
	// EqItemID is the game's own item id. Zero means the server doesn't know
	// it yet; see MageloLookup.NeedIDs.
	EqItemID int `json:"eq_item_id,omitempty"`
	// DKP purchase overview (lookup-only; filled server-side from loot_records).
	DkpCount  int    `json:"dkp_count,omitempty"`
	DkpMedian int    `json:"dkp_median,omitempty"`
	DkpLast   int    `json:"dkp_last,omitempty"`
	DkpLastAt string `json:"dkp_last_at,omitempty"`
	DkpTrend  int    `json:"dkp_trend,omitempty"`
	// Quest turn-in, when this item is a quest reward (lookup-only, like the
	// DKP fields). Quest gear is handed out for components rather than
	// auctioned, so it has no sale history of its own — these are what it cost.
	// QuestValue is the components' total, already resolved through any chain.
	QuestName string `json:"quest_name,omitempty"`
	// QuestFaction is every standing the quest's steps require, joined.
	QuestFaction string `json:"quest_faction,omitempty"`
	// QuestClass restricts the quest to one class. QuestWiki is the walkthrough
	// this summarises. QuestSteps is how many steps it takes. QuestPlat is coin
	// demanded across those steps, and is NOT part of QuestValue — platinum and
	// DKP are different currencies.
	QuestClass string `json:"quest_class,omitempty"`
	QuestWiki  string `json:"quest_wiki,omitempty"`
	QuestSteps int    `json:"quest_steps,omitempty"`
	QuestPlat  int    `json:"quest_plat,omitempty"`
	// QuestCycle is the other items this one can be traded for. All cost the
	// same to reach, and a character can only hold one of them.
	QuestCycle []string `json:"quest_cycle,omitempty"`
	// QuestRoutes is how many quests produce this item — more than 1 means
	// QuestItems shows only the cheapest route. Non-zero for every quest
	// reward, so the UI keys the quest block off it rather than off the
	// shopping list, which is legitimately empty for a quest that needs nothing
	// from outside.
	QuestRoutes int `json:"quest_routes,omitempty"`
	// QuestPriced says whether the shopping list carries resolved prices, so an
	// entry priced at nothing can be told from one never examined. True for
	// every quest reward: components are priced even when the item has its own
	// sale history, because an epic's nominal 1 DKP is not what it cost.
	QuestPriced bool `json:"quest_priced,omitempty"`
	// QuestItems is what the quest needs from OUTSIDE, with everything its own
	// steps produce already netted out. QuestValue is their total.
	QuestItems []QuestComponent `json:"quest_items,omitempty"`
	QuestValue int              `json:"quest_value,omitempty"`
}

// MageloLookup is what LookupItems returns to the frontend.
type MageloLookup struct {
	Items   map[string]MageloItem `json:"items"`    // lower(name) → item
	Missing []string              `json:"missing"`  // unknown names, queued for scraping
	IconURL string                `json:"icon_url"` // base URL; append the icon filename
	// NeedIDs are names the server has no game item id for. A client looking at
	// a real inventory dump can see those ids and nothing else can — the wiki
	// almost never publishes them — so it answers with ReportItemIDs. The list
	// shrinks as ids are recorded and then stops.
	NeedIDs []string `json:"need_ids"`
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
		return MageloLookup{}, fmt.Errorf("officers only")
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
	if out.NeedIDs == nil {
		out.NeedIDs = []string{}
	}
	out.IconURL = base + "/itemicon?f="
	return out, nil
}

// ReportItemIDs sends the server game item ids read out of an inventory dump,
// for names it said it needed (MageloLookup.NeedIDs).
//
// Player inventories are the only source for these: the wiki almost never
// publishes an item id, and an inventory nobody harvested is gone. They matter
// because names are not unique — the Veeshan's Peak key quest has nine separate
// items all called "Piece of a Medallion", and only the id tells them apart.
//
// Best-effort by design. It reports what one character happened to be carrying,
// so a failure means the next inventory opened tries again; nothing depends on
// any single call succeeding, and the caller ignores the error.
func (a *App) ReportItemIDs(ids map[string]int) error {
	if len(ids) == 0 {
		return nil
	}
	return mageloPost("/items/ids", map[string]any{"ids": ids}, nil)
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

// NoteLevitate tells the server a levitation buff was applied on a magelo
// profile, so the configured guild admin gets a note in their Shared mailbox.
//
// NOTE: buff selection is otherwise entirely local (localStorage) — this is the
// only case where toggling a buff leaves the machine, and there is no setting
// that turns it off. Added at the guild admin's request; it reports the
// character name and the buff name, nothing else. Fire-and-forget: the caller
// ignores failures because nothing about the UI depends on it.
func (a *App) NoteLevitate(charName, buffName string) {
	if !IsLinked() || strings.TrimSpace(charName) == "" {
		return
	}
	go func() {
		base := strings.TrimSuffix(serverURL, "/submit")
		body, _ := json.Marshal(map[string]string{"toon": charName, "buff": buffName})
		req, err := http.NewRequest(http.MethodPost, base+"/magelo/levitate", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authHeader())
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()
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

// MageloSlot is one slot of a saved magelo, as the editor exchanges them.
type MageloSlot struct {
	Slot  string `json:"slot"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListMagelos returns a character's saved magelo names (excluding the
// read-only "current" default).
func (a *App) ListMagelos(charName string) ([]string, error) {
	var out struct {
		Magelos []string `json:"magelos"`
	}
	if err := mageloPost("/magelo/list", map[string]any{"toon": charName}, &out); err != nil {
		return nil, err
	}
	if out.Magelos == nil {
		out.Magelos = []string{}
	}
	return out.Magelos, nil
}

// LoadMagelo returns one saved magelo's slots.
func (a *App) LoadMagelo(charName, name string) ([]MageloSlot, error) {
	var out struct {
		Slots []MageloSlot `json:"slots"`
	}
	if err := mageloPost("/magelo/get", map[string]any{"toon": charName, "magelo": name}, &out); err != nil {
		return nil, err
	}
	if out.Slots == nil {
		out.Slots = []MageloSlot{}
	}
	return out.Slots, nil
}

// SaveMageloSlots saves an editor-built magelo under name ("current" is
// reserved for the outputfile snapshot).
func (a *App) SaveMageloSlots(charName, name string, slots []MageloSlot) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "current") {
		return fmt.Errorf("that magelo name is reserved")
	}
	return mageloPost("/magelo/save", map[string]any{
		"toon": charName, "magelo": name, "slots": slots,
	}, nil)
}

// RenameMagelo renames a saved magelo.
func (a *App) RenameMagelo(charName, from, to string) error {
	return mageloPost("/magelo/rename", map[string]any{
		"toon": charName, "from": from, "to": to,
	}, nil)
}

// DeleteMagelo removes a saved magelo ("current" is reserved).
func (a *App) DeleteMagelo(charName, name string) error {
	return mageloPost("/magelo/delete", map[string]any{
		"toon": charName, "magelo": name,
	}, nil)
}

// SearchItems is the magelo editor's slot autofill: name substring search
// filtered server-side to the character's class/race and the worn slot.
func (a *App) SearchItems(q, slot, class, race string) ([]string, error) {
	var out struct {
		Names []string `json:"names"`
	}
	if err := mageloPost("/items/search", map[string]any{
		"q": q, "slot": slot, "class": class, "race": race,
	}, &out); err != nil {
		return nil, err
	}
	if out.Names == nil {
		out.Names = []string{}
	}
	return out.Names, nil
}

// MageloBuff mirrors the server's buff catalog entry. Mod keys: hp, mana,
// ac, atk, haste, ds, hpregen, str, sta, agi, dex, wis, int, cha,
// svf, svc, svd, svp, svm.
type MageloBuff struct {
	Name      string             `json:"name"`
	Icon      string             `json:"icon"`
	Mods      map[string]float64 `json:"mods"`
	Conflicts string             `json:"conflicts"`
	Note      string             `json:"note"`
}

// ListBuffs returns the server's buff catalog for the "+ Buff" picker.
func (a *App) ListBuffs() ([]MageloBuff, error) {
	var out struct {
		Buffs []MageloBuff `json:"buffs"`
	}
	if err := mageloPost("/magelo/buffs", map[string]any{}, &out); err != nil {
		return nil, err
	}
	if out.Buffs == nil {
		out.Buffs = []MageloBuff{}
	}
	return out.Buffs, nil
}

// PreviewBuff scrapes a buff's wiki spell page server-side and returns the
// parsed draft (stats, icon, stacking note, conflict suggestions) without
// saving anything — the Add Buffs dialog prefills from it. Pass the pasted
// page link as pageURL, or a spell name alone (the server builds the URL,
// spaces → underscores).
func (a *App) PreviewBuff(name string, pageURL string) (MageloBuff, error) {
	var out struct {
		Buff MageloBuff `json:"buff"`
	}
	if err := mageloPost("/magelo/buffs/preview", map[string]any{"name": name, "url": pageURL}, &out); err != nil {
		return MageloBuff{}, err
	}
	if out.Buff.Mods == nil {
		out.Buff.Mods = map[string]float64{}
	}
	return out.Buff, nil
}

// SaveBuff upserts one buff definition (admin edit).
func (a *App) SaveBuff(b MageloBuff) error {
	return mageloPost("/magelo/buffs/save", map[string]any{"buff": b}, nil)
}

// DeleteBuff removes one buff definition (admin edit).
func (a *App) DeleteBuff(name string) error {
	return mageloPost("/magelo/buffs/delete", map[string]any{"name": name}, nil)
}

// LibraryEntry is one shared magelo in the Fuse Magelo Library. Name is the
// sharer-chosen display name; Tags is a comma-separated subset of the fixed
// vocabulary (BIS, Value, Twink, Solo, Starter, Nag/Vox).
type LibraryEntry struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	Tags     string `json:"tags"`
	Magelo   string `json:"magelo"`
	Toon     string `json:"toon"`
	Class    string `json:"class"`
	Race     string `json:"race"`
	Level    int    `json:"level"`
	SharedBy string `json:"shared_by"`
	// Handle is the sharer's Discord handle (SharedBy may be a display name).
	Handle string `json:"handle"`
	Votes  int    `json:"votes"`
	MyVote bool   `json:"my_vote"`
	Mine   bool   `json:"mine"`
	// Dynamic entries are the "current" magelo and follow the character's gear
	// on their own; static entries are frozen snapshots of a named magelo.
	Dynamic bool `json:"dynamic"`
	// Diverged: the sharer has edited the source magelo since sharing, so the
	// library shows a build they've moved on from. Static, own entries only.
	Diverged bool `json:"diverged"`
	// DkpTotal is the server's worn-set cost estimate (sale medians, quest
	// component cost as fallback) — what the DKP filter chips bucket on.
	DkpTotal int `json:"dkp_total"`
	// Auto marks a listing nobody shared: a confirmed-52+ character's live
	// "current" gear, published automatically unless its owner opted out.
	// No votes, no delete — there is no library row behind it.
	Auto bool `json:"auto"`
}

// LibraryList is the listing plus the caller's own auto-share preference.
type LibraryList struct {
	Entries []LibraryEntry `json:"entries"`
	// AutoShare is this member's "Share 52+ Magelos" setting (default true).
	AutoShare bool `json:"auto_share"`
}

// ShareMageloToLibrary publishes one of this character's saved magelos
// (pass "" or "current" for the outputfile snapshot) to the shared library
// under a chosen display name (50 chars max) with optional tags. The server
// snapshots the worn + General slots it already has saved.
//
// Sharing "current" publishes a live view — it keeps tracking the character's
// gear afterwards, so it is shared once and never needs re-sharing. A named
// magelo publishes a frozen snapshot; re-sharing one with different gear
// replaces it and resets its votes, which is what confirm acknowledges. The
// server rejects the re-share without it, so the caller can use a failed
// unconfirmed attempt to decide whether to ask.
func (a *App) ShareMageloToLibrary(charName, magelo, name string, tags []string, race string, level int, confirm bool) error {
	return mageloPost("/magelo/library/share", map[string]any{
		"toon": charName, "magelo": magelo, "name": name, "tags": tags,
		"race": race, "level": level, "confirm": confirm,
	}, nil)
}

// ListLibrary returns every shared magelo (curated builds plus automatic 52+
// live listings) with vote counts, sorted by votes, along with the caller's
// own auto-share preference. query ("" = everything) filters server-side over
// toon name, display name, member name/handle, class, tags, and worn item
// names — items are the reason this can't be a client-side filter.
func (a *App) ListLibrary(query string) (LibraryList, error) {
	var out LibraryList
	if err := mageloPost("/magelo/library/list", map[string]any{"q": query}, &out); err != nil {
		return LibraryList{}, err
	}
	if out.Entries == nil {
		out.Entries = []LibraryEntry{}
	}
	return out, nil
}

// GetMageloAutoShare reads the caller's "Share 52+ Magelos" preference —
// whether their characters' live "current" gear is listed automatically.
func (a *App) GetMageloAutoShare() (bool, error) {
	var out struct {
		AutoShare bool `json:"auto_share"`
	}
	if err := mageloPost("/magelo/library/autoshare", map[string]any{}, &out); err != nil {
		return true, err
	}
	return out.AutoShare, nil
}

// SetMageloAutoShare flips the caller's "Share 52+ Magelos" preference.
func (a *App) SetMageloAutoShare(on bool) error {
	return mageloPost("/magelo/library/autoshare", map[string]any{"on": on}, nil)
}

// GetLibraryMagelo returns one library entry's slot snapshot.
func (a *App) GetLibraryMagelo(id int) ([]MageloSlot, error) {
	var out struct {
		Slots []MageloSlot `json:"slots"`
	}
	if err := mageloPost("/magelo/library/get", map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Slots == nil {
		out.Slots = []MageloSlot{}
	}
	return out.Slots, nil
}

// LibraryVoteResult is the post-toggle vote state for one entry.
type LibraryVoteResult struct {
	Votes  int  `json:"votes"`
	MyVote bool `json:"my_vote"`
}

// VoteLibrary sets (up) or clears the caller's thumbs-up on an entry.
func (a *App) VoteLibrary(id int, up bool) (LibraryVoteResult, error) {
	var out LibraryVoteResult
	if err := mageloPost("/magelo/library/vote", map[string]any{"id": id, "up": up}, &out); err != nil {
		return LibraryVoteResult{}, err
	}
	return out, nil
}

// DeleteLibraryEntry removes a shared magelo (owner or officer).
func (a *App) DeleteLibraryEntry(id int) error {
	return mageloPost("/magelo/library/delete", map[string]any{"id": id}, nil)
}

// ItemGetResult is the Add Item dialog's dedup-check response.
type ItemGetResult struct {
	Found bool       `json:"found"`
	Item  MageloItem `json:"item"`
}

// GetItemByName fetches one item record by name (case and apostrophe
// insensitive). Unlike LookupItems this never queues a wiki scrape — it's
// the Add Item dialog's "is this already in the DB?" check.
func (a *App) GetItemByName(name string) (ItemGetResult, error) {
	var out ItemGetResult
	if err := mageloPost("/items/get", map[string]any{"name": name}, &out); err != nil {
		return ItemGetResult{}, err
	}
	return out, nil
}

// IconList carries the server's cached icon filenames plus the URL base the
// frontend prepends to render them.
type IconList struct {
	Icons   []string `json:"icons"`
	IconURL string   `json:"icon_url"`
}

// ListItemIcons returns every icon in the server's cache for the Add Item
// dialog's visual icon browser.
func (a *App) ListItemIcons() (IconList, error) {
	var out IconList
	if err := mageloPost("/itemicon/list", map[string]any{}, &out); err != nil {
		return IconList{}, err
	}
	if out.Icons == nil {
		out.Icons = []string{}
	}
	out.IconURL = strings.TrimSuffix(serverURL, "/submit") + "/itemicon?f="
	return out, nil
}

// TopItem is one row of the magelo editor's top-100 stat table.
type TopItem struct {
	Name    string `json:"name"`
	HP      int    `json:"hp"`
	Mana    int    `json:"mana"`
	Sta     int    `json:"sta"`
	Int     int    `json:"int"`
	Wis     int    `json:"wis"`
	Cha     int    `json:"cha"`
	SvMagic int    `json:"sv_magic"`
}

// TopItemsResult carries the ranked rows plus which stat ranked them
// ("mana" for casters/priests, "hp" otherwise).
type TopItemsResult struct {
	Sort  string    `json:"sort"`
	Items []TopItem `json:"items"`
}

// TopItems returns the top 100 usable items for a slot, ranked by mana for
// casters and priests and by hp for all other classes.
func (a *App) TopItems(slot, class, race string) (TopItemsResult, error) {
	var out TopItemsResult
	if err := mageloPost("/items/top", map[string]any{
		"slot": slot, "class": class, "race": race,
	}, &out); err != nil {
		return TopItemsResult{}, err
	}
	if out.Items == nil {
		out.Items = []TopItem{}
	}
	return out, nil
}

// FailedScrape is one item name the auto-scraper has failed on 3+ times —
// the admin adds these to the DB manually.
type FailedScrape struct {
	Name  string `json:"name"`
	Fails int    `json:"fails"`
	Error string `json:"error"`
	At    string `json:"at"`
}

// ListFailedScrapes returns the failed-scrape ledger (admin view).
func (a *App) ListFailedScrapes() ([]FailedScrape, error) {
	var out struct {
		Failures []FailedScrape `json:"failures"`
	}
	if err := mageloPost("/items/failures", map[string]any{}, &out); err != nil {
		return nil, err
	}
	if out.Failures == nil {
		out.Failures = []FailedScrape{}
	}
	return out.Failures, nil
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

// StubResult is what StubItems did with each name it was given.
type StubResult struct {
	Created  []string          `json:"created"`
	Existing []string          `json:"existing"`
	Failed   map[string]string `json:"failed"` // name → error
}

// StubItems creates placeholder eqitems rows — name and wiki link only — for
// items the wiki has no page for, so a quest can reference them at all
// (quest_step_items has a foreign key into eqitems). Names that already have a
// row come back under Existing untouched; a real item is never overwritten.
func (a *App) StubItems(names []string) (StubResult, error) {
	var out StubResult
	if err := mageloPost("/items/stub", map[string]any{"names": names}, &out); err != nil {
		return StubResult{}, err
	}
	if out.Created == nil {
		out.Created = []string{}
	}
	if out.Existing == nil {
		out.Existing = []string{}
	}
	if out.Failed == nil {
		out.Failed = map[string]string{}
	}
	return out, nil
}

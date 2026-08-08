package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Per-toon quest state: assignments, per-step done marks, and possession-proved
// completions. LOCAL-FIRST — this store is the authority for the UI and every
// operation works unlinked; the server (toonQuests.go) is an opportunistic
// mirror that lets progress follow a character across machines. Writes apply
// here, persist, then post; a post that can't be delivered queues in Pending
// and replays whenever a link exists.
//
// Persistence pattern is trigger_toggles.json: one file, toons keyed by
// lowercased name, a Schema for one-time migrations.

type tqDone struct {
	AtMs   int64  `json:"at_ms"`
	Source string `json:"source"` // manual | loot | inventory | implied
	Detail string `json:"detail,omitempty"`
}

type tqAssign struct {
	Source    string         `json:"source"` // auto | manual
	Removed   bool           `json:"removed,omitempty"`
	StartedMs int64          `json:"started_ms"`
	Done      map[int]tqDone `json:"done"`
}

type tqCompletion struct {
	ItemName string `json:"item_name"`
	AtMs     int64  `json:"at_ms"`
}

// tqHeld is one quest-relevant item from the toon's last inventory scan —
// name, the dump's game item id (what tells the nine medallion pieces apart),
// and where in the bags it sits.
type tqHeld struct {
	Name     string `json:"name"`
	ItemID   int    `json:"item_id,omitempty"`
	Location string `json:"location"`
	Count    int    `json:"count"`
}

type tqToonState struct {
	Assignments map[int]*tqAssign     `json:"assignments"`
	Completions map[int]*tqCompletion `json:"completions"`
	Held        []tqHeld              `json:"held,omitempty"`
	InvModMs    int64                 `json:"inv_mod_ms,omitempty"`
	// synced: a server pull has landed for this toon this session.
	synced bool
}

// tqOp is one queued outbound write, replayed FIFO when linked.
type tqOp struct {
	Kind    string     `json:"kind"` // assign | unassign | progress | complete
	Toon    string     `json:"toon"`
	QuestID int        `json:"quest_id"`
	Source  string     `json:"source,omitempty"`
	Set     []tqOpStep `json:"set,omitempty"`
	Clear   []int      `json:"clear,omitempty"`
	Item    string     `json:"item,omitempty"`
	AtSec   int64      `json:"at,omitempty"`
}

type tqOpStep struct {
	Order  int    `json:"order"`
	Source string `json:"source"`
	Detail string `json:"detail"`
	At     int64  `json:"at,omitempty"`
}

type tqFile struct {
	Schema  int                     `json:"schema"`
	Toons   map[string]*tqToonState `json:"toons"`
	Pending []tqOp                  `json:"pending,omitempty"`
}

const (
	tqSchema        = 1
	tqPendingCap    = 500
	tqSyncStaleness = 5 * time.Minute
)

var (
	tqMu       sync.Mutex
	tqToons    = map[string]*tqToonState{}
	tqPending  []tqOp
	tqLoaded   bool
	tqLastSync time.Time
)

func questProgressPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "questprogress.json")
}

func tqLoadLocked() {
	if tqLoaded {
		return
	}
	tqLoaded = true
	data, err := os.ReadFile(questProgressPath())
	if err != nil {
		return
	}
	var f tqFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	if f.Toons != nil {
		tqToons = f.Toons
	}
	tqPending = f.Pending
	for _, t := range tqToons {
		if t.Assignments == nil {
			t.Assignments = map[int]*tqAssign{}
		}
		if t.Completions == nil {
			t.Completions = map[int]*tqCompletion{}
		}
	}
}

func tqSaveLocked() {
	data, err := json.Marshal(tqFile{Schema: tqSchema, Toons: tqToons, Pending: tqPending})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(questProgressPath()), 0700)
	_ = os.WriteFile(questProgressPath(), data, 0600)
}

func tqStateLocked(toon string) *tqToonState {
	k := strings.ToLower(strings.TrimSpace(toon))
	t := tqToons[k]
	if t == nil {
		t = &tqToonState{Assignments: map[int]*tqAssign{}, Completions: map[int]*tqCompletion{}}
		tqToons[k] = t
	}
	return t
}

// tqEnqueueLocked queues an op for the server, oldest dropped past the cap —
// a machine that stays unlinked forever shouldn't grow a file forever.
func tqEnqueueLocked(op tqOp) {
	tqPending = append(tqPending, op)
	if len(tqPending) > tqPendingCap {
		tqPending = tqPending[len(tqPending)-tqPendingCap:]
	}
}

// tqSend posts one op to the server, returning false when it should stay
// queued (unreachable / unlinked). Callers never hold tqMu here.
func tqSend(op tqOp) bool {
	if !IsLinked() {
		return false
	}
	var out struct {
		OK bool `json:"ok"`
	}
	var err error
	switch op.Kind {
	case "assign":
		err = mageloPost("/quests/assign", map[string]any{
			"toon": op.Toon, "quest_id": op.QuestID, "source": op.Source}, &out)
	case "unassign":
		err = mageloPost("/quests/unassign", map[string]any{
			"toon": op.Toon, "quest_id": op.QuestID}, &out)
	case "progress":
		err = mageloPost("/quests/progress", map[string]any{
			"toon": op.Toon, "quest_id": op.QuestID, "set": op.Set, "clear": op.Clear}, &out)
	case "complete":
		err = mageloPost("/quests/completions/add", map[string]any{
			"toon": op.Toon, "quest_id": op.QuestID, "item_name": op.Item, "at": op.AtSec}, &out)
	case "uncomplete":
		err = mageloPost("/quests/completions/remove", map[string]any{
			"toon": op.Toon, "quest_id": op.QuestID}, &out)
	default:
		return true // unknown op: drop rather than wedge the queue
	}
	return err == nil
}

// tqPost applies-and-persists happened already; this ships the op or queues it.
func tqPost(op tqOp) {
	if tqSend(op) {
		return
	}
	tqMu.Lock()
	tqEnqueueLocked(op)
	tqSaveLocked()
	tqMu.Unlock()
}

// tqFlushPending replays the queue FIFO while the server keeps answering.
func tqFlushPending() {
	for {
		tqMu.Lock()
		tqLoadLocked()
		if len(tqPending) == 0 {
			tqMu.Unlock()
			return
		}
		op := tqPending[0]
		tqMu.Unlock()
		if !tqSend(op) {
			return
		}
		tqMu.Lock()
		if len(tqPending) > 0 {
			tqPending = tqPending[1:]
		}
		tqSaveLocked()
		tqMu.Unlock()
	}
}

// tqPendingTouches reports whether a queued op references (toon, quest) —
// those pairs are locally ahead of the server, so a sync must not overwrite
// them. Caller holds tqMu.
func tqPendingTouchesLocked(toonKey string, questID int) bool {
	for _, op := range tqPending {
		if strings.ToLower(strings.TrimSpace(op.Toon)) == toonKey && op.QuestID == questID {
			return true
		}
	}
	return false
}

// questSyncPoller keeps the background halves current: the catalog refresh
// (6h TTL inside refreshQuestDefs) and the pending-op replay whenever a link
// exists. Hourly — none of this is latency-sensitive.
func questSyncPoller(done <-chan struct{}) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			_ = refreshQuestDefs(false)
			tqFlushPending()
		}
	}
}

// ── server sync ─────────────────────────────────────────────────────────────

// Wire mirror of toonQuests.go's response.
type tqWireStep struct {
	Order  int    `json:"order"`
	DoneAt int64  `json:"done_at"`
	Source string `json:"source"`
	Detail string `json:"detail"`
}
type tqWireAssign struct {
	QuestID   int          `json:"quest_id"`
	Source    string       `json:"source"`
	Removed   bool         `json:"removed"`
	StartedAt int64        `json:"started_at"`
	Steps     []tqWireStep `json:"steps"`
}
type tqWireCompletion struct {
	QuestID     int    `json:"quest_id"`
	ItemName    string `json:"item_name"`
	CompletedAt int64  `json:"completed_at"`
}
type tqWireToon struct {
	Assignments []tqWireAssign     `json:"assignments"`
	Completions []tqWireCompletion `json:"completions"`
}

// syncToonQuests pulls the server's rows for these toons and merges: the
// server wins for any (toon, quest) that has no pending local op — the queue
// holds exactly the truths the server hasn't heard yet.
func syncToonQuests(names []string) {
	if !IsLinked() || len(names) == 0 {
		return
	}
	tqFlushPending()
	var out struct {
		Toons map[string]tqWireToon `json:"toons"`
	}
	if err := mageloPost("/quests/assignments", map[string]any{"toons": names}, &out); err != nil {
		return
	}
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	tqLastSync = time.Now()
	for key, wire := range out.Toons {
		st := tqStateLocked(key)
		st.synced = true
		for _, wa := range wire.Assignments {
			if tqPendingTouchesLocked(key, wa.QuestID) {
				continue
			}
			a := st.Assignments[wa.QuestID]
			if a == nil {
				a = &tqAssign{Done: map[int]tqDone{}}
				st.Assignments[wa.QuestID] = a
			}
			a.Source, a.Removed, a.StartedMs = wa.Source, wa.Removed, wa.StartedAt*1000
			for _, ws := range wa.Steps {
				if _, have := a.Done[ws.Order]; !have {
					a.Done[ws.Order] = tqDone{AtMs: ws.DoneAt * 1000, Source: ws.Source, Detail: ws.Detail}
				}
			}
		}
		for _, wc := range wire.Completions {
			if tqPendingTouchesLocked(key, wc.QuestID) {
				continue
			}
			if _, have := st.Completions[wc.QuestID]; !have {
				st.Completions[wc.QuestID] = &tqCompletion{ItemName: wc.ItemName, AtMs: wc.CompletedAt * 1000}
			}
		}
	}
	tqSaveLocked()
}

// ── core operations (local apply + persist + post) ──────────────────────────

// tqAssignQuest assigns; auto respects a tombstone, manual clears one.
// Reports whether anything changed.
func tqAssignQuest(toon string, questID int, source string) bool {
	if source != "auto" {
		source = "manual"
	}
	tqMu.Lock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	a := st.Assignments[questID]
	switch {
	case a == nil:
		st.Assignments[questID] = &tqAssign{
			Source: source, StartedMs: time.Now().UnixMilli(), Done: map[int]tqDone{},
		}
	case a.Removed && source == "manual":
		a.Removed = false
	default:
		tqMu.Unlock()
		return false // already live, or auto vs tombstone
	}
	tqSaveLocked()
	tqMu.Unlock()
	tqPost(tqOp{Kind: "assign", Toon: toon, QuestID: questID, Source: source})
	return true
}

// tqUnassignQuest tombstones an assignment, reporting whether it was live.
func tqUnassignQuest(toon string, questID int) bool {
	tqMu.Lock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	a := st.Assignments[questID]
	if a == nil || a.Removed {
		tqMu.Unlock()
		return false
	}
	a.Removed = true
	tqSaveLocked()
	tqMu.Unlock()
	tqPost(tqOp{Kind: "unassign", Toon: toon, QuestID: questID})
	return true
}

// tqHasCompletion reports whether a completion is on record for (toon, quest).
func tqHasCompletion(toon string, questID int) bool {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	_, have := tqStateLocked(toon).Completions[questID]
	return have
}

// tqMarkSteps records done marks (first mark wins; absence never unticks) and
// optionally clears exactly the given orders. Returns the orders newly set.
func tqMarkSteps(toon string, questID int, set []tqOpStep, clear []int) []int {
	tqMu.Lock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	a := st.Assignments[questID]
	if a == nil || a.Removed {
		tqMu.Unlock()
		return nil
	}
	var added []int
	var wireSet []tqOpStep
	for _, s := range set {
		if _, have := a.Done[s.Order]; have {
			continue
		}
		at := s.At
		if at <= 0 {
			at = time.Now().Unix()
		}
		a.Done[s.Order] = tqDone{AtMs: at * 1000, Source: s.Source, Detail: s.Detail}
		added = append(added, s.Order)
		s.At = at
		wireSet = append(wireSet, s)
	}
	for _, o := range clear {
		delete(a.Done, o)
	}
	changed := len(added) > 0 || len(clear) > 0
	if changed {
		tqSaveLocked()
	}
	tqMu.Unlock()
	if changed {
		tqPost(tqOp{Kind: "progress", Toon: toon, QuestID: questID, Set: wireSet, Clear: clear})
	}
	return added
}

// tqRecordCompletion logs a possession-proved completion; first date sticks.
// Reports whether it is new.
func tqRecordCompletion(toon string, questID int, item string, atSec int64) bool {
	tqMu.Lock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	if _, have := st.Completions[questID]; have {
		tqMu.Unlock()
		return false
	}
	if atSec <= 0 {
		atSec = time.Now().Unix()
	}
	st.Completions[questID] = &tqCompletion{ItemName: item, AtMs: atSec * 1000}
	tqSaveLocked()
	tqMu.Unlock()
	tqPost(tqOp{Kind: "complete", Toon: toon, QuestID: questID, Item: item, AtSec: atSec})
	return true
}

// tqRemoveCompletion retracts a completion locally and on the server.
func tqRemoveCompletion(toon string, questID int) bool {
	tqMu.Lock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	if _, have := st.Completions[questID]; !have {
		tqMu.Unlock()
		return false
	}
	delete(st.Completions, questID)
	tqSaveLocked()
	tqMu.Unlock()
	tqPost(tqOp{Kind: "uncomplete", Toon: toon, QuestID: questID})
	return true
}

// questPruneStaleCompletions retracts completions whose recorded item no
// longer proves the quest under the current evidence rules — the Ring War
// case: the quest lists its DKP event loot as rewards, but holding a Crown
// of Narandi proves a purchase, not the Ring 10 quest. Self-healing by
// construction: any future evidence correction unwinds its old mistakes on
// the next pass. Completions whose quest the catalog no longer holds, or
// that carry no item name, are left alone — no evidence to judge them by.
func questPruneStaleCompletions() {
	if !questCatalogReady() {
		return
	}
	tqMu.Lock()
	tqLoadLocked()
	type victim struct {
		toon string
		qid  int
		item string
	}
	var victims []victim
	for toonKey, st := range tqToons {
		for qid, c := range st.Completions {
			if strings.TrimSpace(c.ItemName) == "" {
				continue
			}
			victims = append(victims, victim{toonKey, qid, c.ItemName})
		}
	}
	tqMu.Unlock()

	for _, v := range victims {
		if _, known := questByID(v.qid); !known {
			continue
		}
		proves := false
		for _, qid := range qdRewardQuests(normalizeItemName(v.item)) {
			if qid == v.qid {
				proves = true
				break
			}
		}
		if proves {
			continue
		}
		if q, ok := questByID(v.qid); ok && tqRemoveCompletion(v.toon, v.qid) {
			addStatus("Quests: retracted %s completion for %s — %s is DKP loot, not quest evidence.",
				q.Name, v.toon, v.item)
		}
	}
}

// tqAssignedQuestIDs returns the toon's live (non-removed) assignments.
func tqAssignedQuestIDs(toon string) []int {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	out := make([]int, 0, len(st.Assignments))
	for id, a := range st.Assignments {
		if !a.Removed {
			out = append(out, id)
		}
	}
	return out
}

// tqSetHeld replaces a toon's scanned quest-relevant items and records the
// dump's ModTime so the poller knows when a fresh /outputfile landed.
func tqSetHeld(toon string, held []tqHeld, modMs int64) {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	st.Held = held
	if modMs > 0 {
		st.InvModMs = modMs
	}
	tqSaveLocked()
}

// tqInvModMs returns the dump ModTime of the toon's last scan.
func tqInvModMs(toon string) int64 {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	return tqStateLocked(toon).InvModMs
}

// tqNoteLootedHeld shows a just-looted medallion piece on the grid before any
// inventory dump can confirm it: appended as a stand-in entry the next real
// scan replaces wholesale.
func tqNoteLootedHeld(toon, itemName string, itemID int) {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	for _, h := range st.Held {
		if h.ItemID == itemID {
			return
		}
	}
	st.Held = append(st.Held, tqHeld{Name: itemName, ItemID: itemID, Location: "Just looted", Count: 1})
	tqSaveLocked()
}

// tqHeldMedallionIDs reports which medallion piece ids the toon holds.
func tqHeldMedallionIDs(toon string) map[int]bool {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	out := map[int]bool{}
	for _, h := range tqStateLocked(toon).Held {
		if h.ItemID != 0 && vpMedallionByID(h.ItemID) != nil {
			out[h.ItemID] = true
		}
	}
	return out
}

// tqToonSynced reports whether a server pull has landed for this toon.
func tqToonSynced(toon string) bool {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	return tqStateLocked(toon).synced
}

// tqStepDone reports whether a step is already marked.
func tqStepDone(toon string, questID, order int) bool {
	tqMu.Lock()
	defer tqMu.Unlock()
	tqLoadLocked()
	st := tqStateLocked(toon)
	a := st.Assignments[questID]
	if a == nil {
		return false
	}
	_, done := a.Done[order]
	return done
}

// ── bound methods (the Quests sub-tab's API) ────────────────────────────────

type ToonQuestStepUI struct {
	Order  int    `json:"order"`
	Done   bool   `json:"done"`
	AtMs   int64  `json:"at_ms,omitempty"`
	Source string `json:"source,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type ToonQuestAssignUI struct {
	Quest     Quest             `json:"quest"`
	Source    string            `json:"source"`
	StartedMs int64             `json:"started_ms"`
	Steps     []ToonQuestStepUI `json:"steps"`
	DoneCount int               `json:"done_count"`
	// UsesMedallions: this quest involves the nine VP medallion pieces, so
	// its expanded card renders the medallion grid — inside the card like any
	// other quest detail, not as a floating section.
	UsesMedallions bool `json:"uses_medallions"`
}

type ToonQuestCompletionUI struct {
	QuestID   int    `json:"quest_id"`
	QuestName string `json:"quest_name"`
	ItemName  string `json:"item_name"`
	AtMs      int64  `json:"at_ms"`
}

type VPMedallionUI struct {
	ItemID   int    `json:"item_id"`
	Rune     string `json:"rune"`
	Piece    string `json:"piece"`
	Source   string `json:"source"`
	Zone     string `json:"zone"`
	TurnIn   string `json:"turn_in"`
	Held     bool   `json:"held"`
	Location string `json:"location,omitempty"`
	// RuneHeld/RuneLocation: the character holds the COMPLETED medallion this
	// piece belongs to (the hand-in already happened) — the turn-in cell shows
	// the assembled item and where it sits, so collecting lesser pieces and
	// seeing finished medallions coexist on one grid. Uniform across a rune's
	// three piece rows.
	RuneHeld     bool   `json:"rune_held"`
	RuneLocation string `json:"rune_location,omitempty"`
}

type ToonQuestView struct {
	Assignments []ToonQuestAssignUI     `json:"assignments"`
	Completions []ToonQuestCompletionUI `json:"completions"`
	Held        []tqHeld                `json:"held"`
	Medallions  []VPMedallionUI         `json:"medallions"`
	InvAsOfMs   int64                   `json:"inv_as_of_ms"`
	CatalogOK   bool                    `json:"catalog_ok"`
}

// GetToonQuests returns everything the Quests sub-tab renders for one
// character: live assignments joined to their cached definitions, the
// completed-quests log, quest-relevant held items with bag locations, and the
// medallion grid when a medallion quest is on the books. Works unlinked; a
// linked client refreshes from the server behind a small staleness window.
func (a *App) GetToonQuests(name string) ToonQuestView {
	name = strings.TrimSpace(name)
	out := ToonQuestView{
		Assignments: []ToonQuestAssignUI{}, Completions: []ToonQuestCompletionUI{},
		Held: []tqHeld{}, Medallions: []VPMedallionUI{}, CatalogOK: questCatalogReady(),
	}
	if name == "" {
		return out
	}
	if IsLinked() && time.Since(tqLastSync) > tqSyncStaleness {
		syncToonQuests([]string{name})
	}

	tqMu.Lock()
	tqLoadLocked()
	st := tqStateLocked(name)
	assignIDs := make([]int, 0, len(st.Assignments))
	for id, asn := range st.Assignments {
		if !asn.Removed {
			assignIDs = append(assignIDs, id)
		}
	}
	compIDs := make([]int, 0, len(st.Completions))
	for id := range st.Completions {
		compIDs = append(compIDs, id)
	}
	held := append([]tqHeld{}, st.Held...)
	invAsOf := st.InvModMs
	tqMu.Unlock()

	out.Held, out.InvAsOfMs = held, invAsOf

	sort.Ints(assignIDs)
	showMedallions := false
	for _, id := range assignIDs {
		q, ok := questByID(id)
		if !ok {
			continue // definition not cached (catalog empty or quest deleted)
		}
		tqMu.Lock()
		asn := tqStateLocked(name).Assignments[id]
		ui := ToonQuestAssignUI{Quest: q, Source: asn.Source, StartedMs: asn.StartedMs,
			Steps: make([]ToonQuestStepUI, len(q.Steps))}
		for i := range q.Steps {
			ui.Steps[i] = ToonQuestStepUI{Order: i}
			// Orders past the current step count (an officer shortened the
			// quest) are dropped here rather than shown against nothing.
			if d, done := asn.Done[i]; done {
				ui.Steps[i].Done = true
				ui.Steps[i].AtMs, ui.Steps[i].Source, ui.Steps[i].Detail = d.AtMs, d.Source, d.Detail
				ui.DoneCount++
			}
		}
		tqMu.Unlock()
		ui.UsesMedallions = questUsesMedallions(&q)
		if ui.UsesMedallions {
			showMedallions = true
		}
		out.Assignments = append(out.Assignments, ui)
	}

	sort.Ints(compIDs)
	for _, id := range compIDs {
		tqMu.Lock()
		c := tqStateLocked(name).Completions[id]
		tqMu.Unlock()
		qname := fmt.Sprintf("Quest #%d", id)
		if q, ok := questByID(id); ok {
			qname = q.Name
		}
		out.Completions = append(out.Completions, ToonQuestCompletionUI{
			QuestID: id, QuestName: qname, ItemName: c.ItemName, AtMs: c.AtMs,
		})
	}

	// The medallion grid shows for a medallion quest — or the moment any
	// piece turns up in the bags, assigned or not.
	heldByID := map[int]tqHeld{}
	for _, h := range held {
		if h.ItemID != 0 && vpMedallionByID(h.ItemID) != nil {
			heldByID[h.ItemID] = h
			showMedallions = true
		}
	}
	if showMedallions {
		// Completed medallions in the bags, by the assembled item's name —
		// "Medallion of the Jarsath" is unique, unlike its pieces.
		heldByName := map[string]tqHeld{}
		for _, h := range held {
			heldByName[normalizeItemName(h.Name)] = h
		}
		for _, m := range vpMedallions {
			ui := VPMedallionUI{ItemID: m.ID, Rune: m.Rune, Piece: m.Piece,
				Source: m.Source, Zone: m.Zone, TurnIn: m.TurnIn}
			if h, ok := heldByID[m.ID]; ok {
				ui.Held, ui.Location = true, h.Location
			}
			if h, ok := heldByName[normalizeItemName(m.Rune)]; ok {
				ui.RuneHeld, ui.RuneLocation = true, h.Location
			}
			out.Medallions = append(out.Medallions, ui)
		}
	}
	return out
}

// AssignToonQuest is the manual add — it also clears a tombstone, restoring
// whatever progress the earlier run had earned.
func (a *App) AssignToonQuest(name string, questID int) error {
	name = strings.TrimSpace(name)
	if name == "" || questID <= 0 {
		return fmt.Errorf("character and quest are required")
	}
	if _, ok := questByID(questID); !ok {
		return fmt.Errorf("unknown quest")
	}
	tqAssignQuest(name, questID, "manual")
	return nil
}

// UnassignToonQuest tombstones an assignment; an auto-assigned epic will not
// come back on its own.
func (a *App) UnassignToonQuest(name string, questID int) error {
	name = strings.TrimSpace(name)
	if name == "" || questID <= 0 {
		return fmt.Errorf("character and quest are required")
	}
	tqUnassignQuest(name, questID)
	return nil
}

// SetToonQuestStep ticks or unticks one step by hand. A tick auto-ticks the
// steps it implies (the back-trace); an untick clears only the clicked step —
// deciding you haven't done step 9 says nothing about step 2.
func (a *App) SetToonQuestStep(name string, questID, stepOrder int, done bool) error {
	name = strings.TrimSpace(name)
	if name == "" || questID <= 0 || stepOrder < 0 {
		return fmt.Errorf("character, quest and step are required")
	}
	if !done {
		tqMarkSteps(name, questID, nil, []int{stepOrder})
		return nil
	}
	set := []tqOpStep{{Order: stepOrder, Source: "manual"}}
	if q, ok := questByID(questID); ok {
		for _, imp := range impliedStepOrders(&q, stepOrder) {
			set = append(set, tqOpStep{Order: imp, Source: "implied"})
		}
	}
	tqMarkSteps(name, questID, set, nil)
	return nil
}

// ListAssignableQuests returns the catalog entries this character could add:
// classless quests plus their own class's, minus what's already live.
func (a *App) ListAssignableQuests(name string) []Quest {
	name = strings.TrimSpace(name)
	class := strings.ToLower(strings.TrimSpace(classForCharacter(name)))
	live := map[int]bool{}
	for _, id := range tqAssignedQuestIDs(name) {
		live[id] = true
	}
	qdMu.Lock()
	defer qdMu.Unlock()
	out := []Quest{}
	for i := range qdQuests {
		q := &qdQuests[i]
		if live[q.ID] {
			continue
		}
		qc := strings.ToLower(strings.TrimSpace(q.Class))
		if qc != "" && qc != class {
			continue
		}
		out = append(out, *q)
	}
	return out
}

// RefreshQuestDefs re-fetches the catalog on demand (the sub-tab's refresh).
func (a *App) RefreshQuestDefs() error {
	return refreshQuestDefs(true)
}

// ── "also held by" (item hover) ─────────────────────────────────────────────

type ItemHolderUI struct {
	Char  string `json:"char"`
	Count int    `json:"count"`
	Where string `json:"where"`
	// pieceID carries the medallion piece id through the shared cache; only
	// WhoHasMedallionPieces sets or reads it.
	pieceID int
}

var (
	whoHasMu    sync.Mutex
	whoHasCache = map[string]struct {
		at   time.Time
		hits []ItemHolderUI
	}{}
)

const whoHasTTL = 60 * time.Second

// WhoHasMedallionPieces reports which of the user's own characters hold each
// VP medallion piece, keyed by piece item id — the nine pieces share one NAME,
// so a name lookup would blur them together; the dump's item id is the only
// thing that tells a Kylong Upper from a Jarsath Bottom. One pass over the
// local dumps, cached briefly (hover-driven).
func (a *App) WhoHasMedallionPieces() map[int][]ItemHolderUI {
	whoHasMu.Lock()
	if c, ok := whoHasCache["\x00medallions"]; ok && time.Since(c.at) < whoHasTTL {
		whoHasMu.Unlock()
		out := map[int][]ItemHolderUI{}
		for _, h := range c.hits {
			out[h.pieceID] = append(out[h.pieceID], h)
		}
		return out
	}
	whoHasMu.Unlock()

	eqDir := GetSettings().EQDirectory
	var flat []ItemHolderUI
	for _, char := range logFileCharNames(eqDir) {
		perPiece := map[int]int{}
		perWhere := map[int]map[string]bool{}
		seen := map[string]int{}
		for _, it := range readInventoryItems(char, eqDir) {
			if normalizeItemName(it.Name) != vpMedallionItemName || vpMedallionByID(it.ItemID) == nil {
				continue
			}
			n := it.Count
			if n <= 0 {
				n = 1
			}
			perPiece[it.ItemID] += n
			where, _ := describeInvLocation(it.Location, seen)
			if perWhere[it.ItemID] == nil {
				perWhere[it.ItemID] = map[string]bool{}
			}
			perWhere[it.ItemID][where] = true
		}
		for id, count := range perPiece {
			ws := make([]string, 0, len(perWhere[id]))
			for w := range perWhere[id] {
				ws = append(ws, w)
			}
			sort.Strings(ws)
			flat = append(flat, ItemHolderUI{Char: char, Count: count, Where: strings.Join(ws, ", "), pieceID: id})
		}
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].Char < flat[j].Char })

	whoHasMu.Lock()
	whoHasCache["\x00medallions"] = struct {
		at   time.Time
		hits []ItemHolderUI
	}{time.Now(), flat}
	whoHasMu.Unlock()

	out := map[int][]ItemHolderUI{}
	for _, h := range flat {
		out[h.pieceID] = append(out[h.pieceID], h)
	}
	return out
}

// WhoHasItem reports which of the USER'S OWN characters hold an item (exact
// name match against each local inventory dump) — never other guildmates, no
// server involved. Hover-driven, so results cache briefly.
func (a *App) WhoHasItem(item string) []ItemHolderUI {
	key := normalizeItemName(item)
	if key == "" {
		return []ItemHolderUI{}
	}
	whoHasMu.Lock()
	if c, ok := whoHasCache[key]; ok && time.Since(c.at) < whoHasTTL {
		whoHasMu.Unlock()
		return c.hits
	}
	whoHasMu.Unlock()

	eqDir := GetSettings().EQDirectory
	hits := []ItemHolderUI{}
	for _, char := range logFileCharNames(eqDir) {
		count := 0
		wheres := map[string]bool{}
		seen := map[string]int{}
		for _, it := range readInventoryItems(char, eqDir) {
			if normalizeItemName(it.Name) != key {
				continue
			}
			n := it.Count
			if n <= 0 {
				n = 1
			}
			count += n
			where, _ := describeInvLocation(it.Location, seen)
			wheres[where] = true
		}
		if count == 0 {
			continue
		}
		ws := make([]string, 0, len(wheres))
		for w := range wheres {
			ws = append(ws, w)
		}
		sort.Strings(ws)
		hits = append(hits, ItemHolderUI{Char: char, Count: count, Where: strings.Join(ws, ", ")})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Char < hits[j].Char })

	whoHasMu.Lock()
	whoHasCache[key] = struct {
		at   time.Time
		hits []ItemHolderUI
	}{time.Now(), hits}
	whoHasMu.Unlock()
	return hits
}

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Automatic quest-progress detection, per character, from the two places item
// possession shows up: loot lines in the live log, and the /outputfile
// inventory dump. Both feed the same note: an item that a step PRODUCES marks
// that step done (plus the steps it implies), and an item that a quest REWARDS
// records a completion. Detection is strictly additive — an item vanishing
// from the bags unticks nothing, because hand-ins consume items by design.

// "--You have looted a Rusty Sword.--" — the article belongs to the line, not
// the item name. Kept tolerant of items whose article reads oddly.
var questLootRE = regexp.MustCompile(`^--You have looted (?:a |an |the )?(.+?)\.--$`)

// RecordQuestLootLine feeds one raw log line; called for every tailed line, so
// the cheap prefix gate comes first.
func RecordQuestLootLine(line string) {
	content := logMessageContent(line)
	if !strings.HasPrefix(content, "--You have looted ") || !strings.HasSuffix(content, ".--") {
		return
	}
	m := questLootRE.FindStringSubmatch(content)
	if m == nil {
		return
	}
	toon := currentCharName
	if toon == "" {
		return
	}
	atSec := int64(0)
	if at := logLineTime(line); !at.IsZero() {
		atSec = at.Unix()
	}
	questNoteItem(toon, m[1], 0, "", "loot", atSec)
}

// questNoteItem is the shared possession event. itemID is the dump's game
// item id when known (inventory), 0 from a loot line — except for the nine
// same-named medallion pieces, where the zone the loot fired in identifies
// the piece (each comes from its own zone).
func questNoteItem(toon, itemName string, itemID int, location, source string, atSec int64) {
	key := normalizeItemName(itemName)
	if key == "" {
		return
	}
	if key == vpMedallionItemName && itemID == 0 && source == "loot" {
		if m := vpMedallionByZone(CurrentZone()); m != nil {
			itemID = m.ID
			// Show it on the medallion grid immediately; the next inventory
			// scan replaces this stand-in with the real bag location.
			tqNoteLootedHeld(toon, itemName, itemID)
			addStatus("Quests: that was the %s piece of the %s (%s).", m.Piece, m.Rune, m.Zone)
		}
	} else if source == "loot" && qdIsInputItem(key) {
		// A hand-in component counts the moment it's looted: feasibility
		// (questStepFeasible) judges by the bags, and waiting for the next
		// /outputfile would keep a now-doable hand-in flagged as not.
		tqNoteLootedHeld(toon, itemName, itemID)
	}
	// A medallion piece in hand retires its zone flag by piece identity.
	if key == vpMedallionItemName && itemID != 0 {
		if m := vpMedallionByID(itemID); m != nil {
			questMarkersRetireLabel(fmt.Sprintf("%s (%s piece)", m.Rune, m.Piece))
		}
	}

	// Step outputs: for each assigned quest producing this item, tick the
	// EARLIEST not-done producing step (same-name collisions within a quest
	// resolve conservatively), plus everything that step implies.
	live := map[int]bool{}
	for _, id := range tqAssignedQuestIDs(toon) {
		live[id] = true
	}
	perQuest := map[int]int{} // quest → earliest producing step still open
	for _, ref := range qdOutputRefs(key) {
		if !live[ref.QuestID] || tqStepDone(toon, ref.QuestID, ref.Step) {
			continue
		}
		if cur, ok := perQuest[ref.QuestID]; !ok || ref.Step < cur {
			perQuest[ref.QuestID] = ref.Step
		}
	}
	for qid, step := range perQuest {
		q, ok := questByID(qid)
		if !ok {
			continue
		}
		set := []tqOpStep{{Order: step, Source: source, Detail: itemName, At: atSec}}
		for _, imp := range impliedStepOrders(&q, step) {
			if !tqStepDone(toon, qid, imp) {
				set = append(set, tqOpStep{Order: imp, Source: "implied", Detail: itemName, At: atSec})
			}
		}
		if added := tqMarkSteps(toon, qid, set, nil); len(added) > 0 {
			addStatus("Quests: %s — step %d done for %s (%s).", q.Name, step+1, toon, itemName)
			// The nudge flag pointing at a now-done step has served its purpose
			// — the item is in hand, so it comes off the map at once instead of
			// waiting for the visited-and-left retirement.
			for _, op := range set {
				questMarkersRetireLabel(questNudgeLabel(&q, op.Order))
			}
		}
	}

	// Reward items: a completion, whether or not the quest was ever assigned.
	for _, qid := range qdRewardQuests(key) {
		if q, ok := questByID(qid); ok {
			if tqRecordCompletion(toon, qid, itemName, atSec) {
				addStatus("Quests: %s holds %s — %s recorded as completed.", toon, itemName, q.Name)
				// Epics are once-only: the item in hand retires the quest
				// assignment on the spot (the auto-assigner won't return it).
				if questIsEpic(&q) && tqUnassignQuest(toon, qid) {
					addStatus("Quests: %s's epic is done — %s removed from their list.", toon, q.Name)
				}
			}
		}
	}
}

// questScanInventory reads a character's dump and notes every quest-relevant
// item: step outputs, reward items, step inputs (hand-in components, for
// feasibility), and medallion pieces — retaining name, game item id, and bag
// location so the UI can say where things live.
func questScanInventory(name string) {
	eqDir := GetSettings().EQDirectory
	items := readInventoryItems(name, eqDir)
	if items == nil {
		return // no dump for this character
	}
	var modMs int64
	if p := eqRootFilePath(eqDir, name+"-Inventory.txt"); p != "" {
		if info, err := os.Stat(p); err == nil {
			modMs = info.ModTime().UnixMilli()
		}
	}

	var held []tqHeld
	for _, it := range items {
		key := normalizeItemName(it.Name)
		if key == "" {
			continue
		}
		relevant := key == vpMedallionItemName ||
			len(qdOutputRefs(key)) > 0 || len(qdRewardQuests(key)) > 0 ||
			qdIsInputItem(key)
		if !relevant {
			continue
		}
		n := it.Count
		if n <= 0 {
			n = 1
		}
		held = append(held, tqHeld{Name: it.Name, ItemID: it.ItemID, Location: it.Location, Count: n})
	}
	tqSetHeld(name, held, modMs)

	noted := map[string]bool{}
	for _, h := range held {
		k := normalizeItemName(h.Name) + "|" + fmt.Sprint(h.ItemID)
		if noted[k] {
			continue
		}
		noted[k] = true
		questNoteItem(name, h.Name, h.ItemID, h.Location, "inventory", 0)
	}

	// Tell the Characters tab this character's inventory-derived state moved:
	// running /outputfile is a deliberate act, and the point of it is watching
	// the app catch up — the tab reloads its panes instead of serving its
	// cache until the next manual tab switch.
	if v3App != nil {
		v3App.Event.Emit("quest-inv-changed", name)
	}
}

// questInvPoller watches the current character's dump for a new /outputfile
// inventory (there is no file event to hook — same ModTime-stat approach as
// the threat meter's haste scan) and rescans when it changes. The tick is
// short because the wait is user-facing: /outputfile is run to see the app
// catch up, and the old one-minute worst case read as detection being broken.
// An idle tick is a single os.Stat.
func questInvPoller(done <-chan struct{}) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			toon := currentCharName
			if toon == "" {
				continue
			}
			p := eqRootFilePath(GetSettings().EQDirectory, toon+"-Inventory.txt")
			if p == "" {
				continue
			}
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if info.ModTime().UnixMilli() > tqInvModMs(toon) {
				questScanInventory(toon)
			}
		}
	}
}

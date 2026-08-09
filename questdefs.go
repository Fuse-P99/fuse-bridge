package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Quest-definition cache: the officer-authored catalog (quests.go wire types),
// fetched from the server and kept on disk so every install — linked or not,
// online or not — can assign and track quests. Same pattern as the Fuse
// trigger cache (fuse_triggers.json): server-owned data, local copy, refresh
// when reachable, never blank on failure.
//
// The derived indexes answer the tracking questions in O(1) per log line:
// which step produces this item, which quest rewards it, which class's epic
// is which quest, and which steps happen in this zone.

type questDefsFile struct {
	FetchedAt time.Time `json:"fetched_at"`
	Quests    []Quest   `json:"quests"`
}

// qdStepRef names one step of one quest.
type qdStepRef struct {
	QuestID int
	Step    int
}

var (
	qdMu         sync.Mutex
	qdQuests     []Quest
	qdFetchedAt  time.Time
	qdByID       map[int]int            // quest id → index into qdQuests
	qdOutputIdx  map[string][]qdStepRef // normalizeItemName(step OUTPUT) → producers
	qdRewardIdx  map[string][]int       // normalizeItemName(reward item) → quest ids
	qdEpicFor    map[string]int         // lower(class) → epic quest id
	qdZoneIdx    map[string][]qdStepRef // normalizeZoneKey(zone) → steps located there
	qdEpicWarned bool
)

const qdRefreshEvery = 6 * time.Hour

var qdEpicRE = regexp.MustCompile(`(?i)\bepic\b`)

func questDefsPath() string {
	return filepath.Join(filepath.Dir(settingsPath()), "questdefs.json")
}

func loadQuestDefs() {
	qdMu.Lock()
	defer qdMu.Unlock()
	data, err := os.ReadFile(questDefsPath())
	if err != nil {
		return
	}
	var f questDefsFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	qdQuests, qdFetchedAt = f.Quests, f.FetchedAt
	qdRebuildLocked()
}

func saveQuestDefsLocked() {
	data, err := json.Marshal(questDefsFile{FetchedAt: qdFetchedAt, Quests: qdQuests})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(questDefsPath()), 0700)
	_ = os.WriteFile(questDefsPath(), data, 0600)
}

// refreshQuestDefs fetches the catalog. The endpoint is unauthenticated
// server-side, so this works for unlinked installs too; a fetch failure keeps
// whatever the cache already holds.
func refreshQuestDefs(force bool) error {
	qdMu.Lock()
	fresh := !force && time.Since(qdFetchedAt) < qdRefreshEvery && len(qdQuests) > 0
	qdMu.Unlock()
	if fresh {
		return nil
	}
	quests, err := (&App{}).ListQuests()
	if err != nil {
		return err
	}
	if len(quests) == 0 {
		return nil // an empty catalog is never an upgrade over a cached one
	}
	qdMu.Lock()
	qdQuests, qdFetchedAt = quests, time.Now()
	qdRebuildLocked()
	saveQuestDefsLocked()
	qdMu.Unlock()
	return nil
}

// qdDkpRiderItems are reward-table entries that are PURCHASED with DKP at the
// event rather than earned by doing the quest — holding one proves a raid
// purchase, not quest completion. The Ring War quests list their event loot
// as rewards (that's what prices the loot in the Magelo tooltip), but only
// the ring itself is handed to everyone who actually finished the quest:
// Ring 10's proof is the Ring of Dain Frostreaver IV and nothing else, and
// Ring 9's is the Coldain Hero's Insignia Ring. Keys are normalized names;
// these are excluded from BOTH completion evidence and step-tick evidence.
var qdDkpRiderItems = map[string]bool{
	"crown of narandi":             true,
	"eye of narandi":               true,
	"earring of the frozen skull":  true,
	"faceguard of bentos the hero": true,
	"choker of the wretched":       true,
	"dirk of the dain":             true,
}

// qdRebuildLocked derives every index from qdQuests. Caller holds qdMu.
func qdRebuildLocked() {
	qdByID = map[int]int{}
	qdOutputIdx = map[string][]qdStepRef{}
	qdRewardIdx = map[string][]int{}
	qdEpicFor = map[string]int{}
	qdZoneIdx = map[string][]qdStepRef{}

	addReward := func(name string, id int) {
		k := normalizeItemName(name)
		if k == "" || qdDkpRiderItems[k] {
			return
		}
		for _, have := range qdRewardIdx[k] {
			if have == id {
				return
			}
		}
		qdRewardIdx[k] = append(qdRewardIdx[k], id)
	}

	for qi := range qdQuests {
		q := &qdQuests[qi]
		qdByID[q.ID] = qi

		// The class's epic. Two candidates would mean a data problem; the
		// lowest id wins deterministically and the status feed says so once.
		if questIsEpic(q) {
			ck := strings.ToLower(strings.TrimSpace(q.Class))
			if prev, ok := qdEpicFor[ck]; !ok || q.ID < prev {
				if ok && !qdEpicWarned {
					qdEpicWarned = true
					addStatus("Quests: more than one epic found for %s — using the oldest entry.", q.Class)
				}
				qdEpicFor[ck] = q.ID
			}
		}

		for si := range q.Steps {
			s := &q.Steps[si]
			ref := qdStepRef{QuestID: q.ID, Step: si}

			for ii := range s.Items {
				if s.Items[ii].Role == "out" {
					k := normalizeItemName(s.Items[ii].Name)
					if k != "" && !qdDkpRiderItems[k] {
						qdOutputIdx[k] = append(qdOutputIdx[k], ref)
					}
				}
			}

			// Zone index: the step's own zone, else everywhere its mobs stand.
			zones := map[string]bool{}
			if z := normalizeZoneKey(s.ZoneName); z != "" {
				zones[z] = true
			} else if z := normalizeZoneKey(s.ZoneID); z != "" {
				zones[z] = true
			}
			for mi := range s.Mobs {
				if z := normalizeZoneKey(s.Mobs[mi].Zone); z != "" {
					zones[z] = true
				}
			}
			for z := range zones {
				qdZoneIdx[z] = append(qdZoneIdx[z], ref)
			}
		}

		// Completion evidence: reward items, every member of a reward cycle,
		// and — for direct-reward quests — the last step's outputs.
		for ri := range q.Rewards {
			r := &q.Rewards[ri]
			if r.Kind == "faction" {
				continue
			}
			addReward(r.Name, q.ID)
			for _, c := range r.Cycle {
				addReward(c, q.ID)
			}
		}
		if q.DirectRewards && len(q.Steps) > 0 {
			last := &q.Steps[len(q.Steps)-1]
			for ii := range last.Items {
				if last.Items[ii].Role == "out" {
					addReward(last.Items[ii].Name, q.ID)
				}
			}
		}
	}
}

// qdOutputRefs returns the steps that produce an item (key already
// normalized), as a copy safe to read without the lock.
func qdOutputRefs(key string) []qdStepRef {
	qdMu.Lock()
	defer qdMu.Unlock()
	return append([]qdStepRef{}, qdOutputIdx[key]...)
}

// qdRewardQuests returns the quests that reward an item (key normalized).
func qdRewardQuests(key string) []int {
	qdMu.Lock()
	defer qdMu.Unlock()
	return append([]int{}, qdRewardIdx[key]...)
}

// qdZoneRefs returns the steps located in a zone (any known spelling).
func qdZoneRefs(zone string) []qdStepRef {
	z := normalizeZoneKey(zone)
	if z == "" {
		return nil
	}
	qdMu.Lock()
	defer qdMu.Unlock()
	return append([]qdStepRef{}, qdZoneIdx[z]...)
}

// questByID returns a copy of one cached quest definition.
func questByID(id int) (Quest, bool) {
	qdMu.Lock()
	defer qdMu.Unlock()
	if qi, ok := qdByID[id]; ok {
		return qdQuests[qi], true
	}
	return Quest{}, false
}

// questCatalogReady reports whether any definitions are cached — the only
// thing the quest UI ever gates on.
func questCatalogReady() bool {
	qdMu.Lock()
	defer qdMu.Unlock()
	return len(qdQuests) > 0
}

// epicQuestForClass returns the class's epic quest id, 0 when unknown.
func epicQuestForClass(class string) int {
	qdMu.Lock()
	defer qdMu.Unlock()
	return qdEpicFor[strings.ToLower(strings.TrimSpace(class))]
}

// questIsEpic reports whether a quest is a class epic — the once-only kind:
// a character can never do theirs twice, so holding the epic item retires the
// quest assignment for good.
func questIsEpic(q *Quest) bool {
	return q.Class != "" && qdEpicRE.MatchString(q.Name)
}

// impliedStepOrders is the Go port of QuestEditor.svelte's impliedSteps: the
// earlier steps that MUST already be done for step i to be done. Two things
// imply a step — an input that exactly one earlier step produces, and the
// `follows` flag (hand in X, a mob spawns, kill it). Where several earlier
// steps could have satisfied a slot the route can't be known and nothing is
// ticked: a wrong tick is worse than a missing one, because it hides work
// still to do.
func impliedStepOrders(q *Quest, i int) []int {
	need := map[int]bool{}
	var visit func(n int)
	visit = func(n int) {
		if n < 0 || n >= len(q.Steps) {
			return
		}
		s := &q.Steps[n]
		if s.Follows && n > 0 && !need[n-1] {
			need[n-1] = true
			visit(n - 1)
		}
		for ii := range s.Items {
			slot := &s.Items[ii]
			if slot.Role == "out" {
				continue
			}
			names := map[string]bool{normalizeItemName(slot.Name): true}
			for _, alt := range slot.Alts {
				names[normalizeItemName(alt)] = true
			}
			producers := []int{}
			for m := 0; m < n; m++ {
				prod := false
				for oi := range q.Steps[m].Items {
					o := &q.Steps[m].Items[oi]
					if o.Role == "out" && names[normalizeItemName(o.Name)] {
						prod = true
						break
					}
				}
				if prod {
					producers = append(producers, m)
				}
			}
			if len(producers) == 1 && !need[producers[0]] {
				need[producers[0]] = true
				visit(producers[0])
			}
		}
	}
	visit(i)
	delete(need, i)
	out := make([]int, 0, len(need))
	for n := range need {
		out = append(out, n)
	}
	return out
}

// ── Veeshan's Peak medallion pieces ─────────────────────────────────────────
//
// Nine items all named "Piece of a Medallion", told apart only by game item
// id — which the inventory dump carries and no log line does. Each piece
// comes from its own zone, so a loot line CAN be identified by where the
// player is standing when it fires. Data from the P99 wiki's Sebilis and
// Veeshan's Peak Key Quest page; ids cross-checked against the known
// 19956–19964 range. Locs are in EQ /loc order (Y, X); a wanderer has none.

const vpMedallionItemName = "piece of a medallion" // normalizeItemName form

type vpMedallion struct {
	ID     int     // game item id
	Rune   string  // which medallion it combines into
	Piece  string  // Upper / Middle / Bottom
	Source string  // how it is obtained
	Zone   string  // canonical zone display name
	LocY   float64 // /loc Y of the spot, when one exists
	LocX   float64
	HasLoc bool
	TurnIn string // who assembles the medallion
}

var vpMedallions = []vpMedallion{
	{19961, "Medallion of the Jarsath", "Upper", "Ground spawn (tiny bag near the waterline by a tree, 8h respawn)", "Swamp of No Hope", 52, 2935, true, "Xiblin Fizzlebik in Timorous Deep (-5778, 2943)"},
	{19960, "Medallion of the Jarsath", "Middle", "Drops from an ancient Jarsath (wanders after spawning — Sense the Dead helps)", "Firiona Vie", 0, 0, false, "Xiblin Fizzlebik in Timorous Deep (-5778, 2943)"},
	{19959, "Medallion of the Jarsath", "Bottom", "Drops from a bloodgill marauder (underwater in front of the temple, 8h respawn)", "Lake of Ill Omen", -378, -57, true, "Xiblin Fizzlebik in Timorous Deep (-5778, 2943)"},
	{19958, "Medallion of the Obulus", "Upper", "Burnished Wooden Staff quest from Ssolet Dnaas", "Warsliks Wood", 4014, 402, true, "Slixin Klex in Burning Woods (-678, -1129)"},
	{19957, "Medallion of the Obulus", "Middle", "Drops from the rotting skeleton", "Dreadlands", 2252, -5144, true, "Slixin Klex in Burning Woods (-678, -1129)"},
	{19956, "Medallion of the Obulus", "Bottom", "Drops from the Pained Soul (near the ramp to the Old Sebilis orb)", "Trakanon's Teeth", -1834, -4368, true, "Slixin Klex in Burning Woods (-678, -1129)"},
	{19964, "Medallion of the Kylong", "Upper", "Niblek's Gems quest from Niblek", "Chardok", 506, -107, true, "Professor Akabao in Lake of Ill Omen (-2540, -2996)"},
	{19963, "Medallion of the Kylong", "Middle", "Drops from Verix Kyloxs Remains (basement)", "Karnor's Castle", 114, -631, true, "Professor Akabao in Lake of Ill Omen (-2540, -2996)"},
	{19962, "Medallion of the Kylong", "Bottom", "Ground spawn (2nd floor of the library, 8h respawn)", "Kaesora", 55, -439, true, "Professor Akabao in Lake of Ill Omen (-2540, -2996)"},
}

// vpMedallionByZone identifies which piece a zone produces — the pieces come
// from nine DISTINCT zones, so the zone a loot line fires in names the piece.
func vpMedallionByZone(zone string) *vpMedallion {
	z := normalizeZoneKey(zone)
	if z == "" {
		return nil
	}
	for i := range vpMedallions {
		if normalizeZoneKey(vpMedallions[i].Zone) == z {
			return &vpMedallions[i]
		}
	}
	return nil
}

// vpMedallionByID identifies a piece from the inventory dump's item id.
func vpMedallionByID(id int) *vpMedallion {
	for i := range vpMedallions {
		if vpMedallions[i].ID == id {
			return &vpMedallions[i]
		}
	}
	return nil
}

// questUsesMedallions reports whether a quest's steps involve the pieces —
// what turns the medallion grid on for an assigned quest.
func questUsesMedallions(q *Quest) bool {
	for si := range q.Steps {
		for ii := range q.Steps[si].Items {
			it := &q.Steps[si].Items[ii]
			if normalizeItemName(it.Name) == vpMedallionItemName {
				return true
			}
			for _, alt := range it.Alts {
				if normalizeItemName(alt) == vpMedallionItemName {
					return true
				}
			}
		}
	}
	return false
}

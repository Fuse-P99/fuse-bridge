package main

import "fmt"

// Quests: a named walkthrough of the ordered steps that produce an item.
// Reading needs a linked client; creating and editing is officer-only and
// enforced server-side — the admin-mode button in the Magelo view only decides
// whether to show the controls, never whether the save lands.
//
// A quest is the whole chain, not one hand-in: a class epic is one quest with
// twenty steps. See quests.go on the server for how that shapes the pricing.

// QuestStepItem is one item slot on a step. Role is "in" (handed over or
// consumed) or "out" (received). A name repeats when a step wants more than one
// — hand-ins take unstacked items, so two Bottles of Milk are two slots.
type QuestStepItem struct {
	Name string `json:"name"`
	// Alts are other items that satisfy this same slot — the Essence Lens quest
	// takes a talisman from any one of four dragons. Name is the first; Alts
	// holds the rest. Empty for the ordinary case of one specific item.
	Alts []string `json:"alts,omitempty"`
	Role string   `json:"role"`
	// ConsumedOK is false for an item handed over and given straight back — the
	// Enchanter's Jeb's Seal returns from all four masters. ConsumedFail is the
	// tradeskill case: a failed combine destroys some components and returns
	// others. Only ConsumedOK is priced; both are recorded.
	ConsumedOK   bool `json:"consumed_ok"`
	ConsumedFail bool `json:"consumed_fail"`
}

// QuestStepMob is one NPC on a step: handed to, talked to, or looted from.
// Name and Zone are resolved for display and ignored on save.
type QuestStepMob struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Zone string `json:"zone"`
}

// QuestStep is one step. Which fields matter depends on Kind:
//
//	handin    one mob, faction, plat, items in and out
//	combine   tradeskill, skill, items in (with consumed flags) and out
//	loot      any number of mobs, items out
//	ground    zone, items out
//	dialogue  one mob, say, faction, items out
type QuestStep struct {
	Kind       string `json:"kind"`
	Tradeskill string `json:"tradeskill"`
	SkillReq   int    `json:"skill_req"`
	// Mobs are the NPCs involved — handed to, talked to, or looted from. A list
	// because an item routinely drops from several; hand-in and dialogue steps
	// hold exactly one, which the server enforces. Names come back resolved for
	// display and are ignored on save, since mob names aren't unique in eqmobs.
	Mobs     []QuestStepMob `json:"mobs"`
	ZoneID   string         `json:"zone_id"`
	ZoneName string         `json:"zone_name"`
	Say      string         `json:"say"`
	// The standing the step REQUIRES. Distinct from a faction REWARD, which is
	// a numeric delta.
	FactionLevel string `json:"faction_level"`
	FactionGroup string `json:"faction_group"`
	// Coin demanded alongside the items, never folded into the DKP figure.
	PlatCost int             `json:"plat_cost"`
	Note     string          `json:"note"`
	Items    []QuestStepItem `json:"items"`
}

// QuestReward is what the quest is worth having. Kind is "item", "faction", or
// "cycle" — several items that are interchangeable outcomes of the same work,
// where the final hand-in gives X, X trades for Y, and Y for Z.
type QuestReward struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	FactionGroup string   `json:"faction_group"`
	FactionDelta int      `json:"faction_delta"`
	Cycle        []string `json:"cycle"`
}

// QuestPrereq is another quest this one requires first. Recorded, not priced:
// where a prerequisite matters to the cost it does so through an item, and the
// item chain already follows that.
type QuestPrereq struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Quest is one whole walkthrough. ID is 0 for one that hasn't been saved yet.
type Quest struct {
	ID      int           `json:"id"`
	Name    string        `json:"name"`
	Class   string        `json:"class"`
	Wiki    string        `json:"wiki_url"`
	Prereqs []QuestPrereq `json:"prereqs"`
	Rewards []QuestReward `json:"rewards"`
	Steps   []QuestStep   `json:"steps"`
}

// QuestComponent is one entry in the tooltip's shopping list: an item the quest
// consumes, priced at its own DKP median. Nothing is discounted for being
// farmable — spending a White Dragon Scale costs you what one is worth whether
// you bought it or killed for it.
type QuestComponent struct {
	// Name is the alternative the price came from — the cheapest, for a slot
	// that accepts several. Alts are the others that would also satisfy it.
	Name      string   `json:"name"`
	Alts      []string `json:"alts,omitempty"`
	DkpCount  int      `json:"dkp_count,omitempty"`
	DkpMedian int      `json:"dkp_median,omitempty"`
	Value     int      `json:"value,omitempty"`
	FromQuest bool     `json:"from_quest,omitempty"`
	// Free marks an item whose own quest demands nothing from outside.
	// Separates "known to cost nothing" from "we have no price".
	Free bool `json:"free,omitempty"`
}

// QuestFactionGroup is one autocomplete entry for a faction field.
type QuestFactionGroup struct {
	Name   string `json:"name"`
	Quests int    `json:"quests"`
}

// QuestVocab is the server's own list of what each dropdown may contain, so the
// editor can't offer a value the save would reject.
type QuestVocab struct {
	StepKinds   []string `json:"step_kinds"`
	Tradeskills []string `json:"tradeskills"`
	Classes     []string `json:"classes"`
	Factions    []string `json:"factions"`
	RewardKinds []string `json:"reward_kinds"`
}

// QuestMob is one NPC: an eqmobs row, with the two columns a quest NPC needs on
// top of what every mob carries. ZoneName is resolved from ZoneID server-side
// for display; ZoneID is what's stored.
type QuestMob struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Nicknames string `json:"nicknames"`
	ZoneID    string `json:"zone_id"`
	ZoneName  string `json:"zone_name"`
	Faction   string `json:"faction"`
	// QuestMob marks the mob as a quest NPC, which floats it to the top of the
	// editor's search. Attaching a mob to a step sets it server-side.
	QuestMob bool `json:"quest_mob"`
}

// QuestZone is one entry in the zone picker on a ground-spawn step.
type QuestZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListQuestVocab returns the dropdown vocabularies.
func (a *App) ListQuestVocab() (QuestVocab, error) {
	var out QuestVocab
	if err := mageloPost("/quests/vocab", map[string]any{}, &out); err != nil {
		return QuestVocab{}, err
	}
	return out, nil
}

// ListQuestFactions returns the faction autocomplete list: a seed roster from
// the P99 faction guide merged with every faction already in use by a step or
// an NPC, ordered so the guild's own factions come first.
func (a *App) ListQuestFactions() ([]QuestFactionGroup, error) {
	var out struct {
		Factions []QuestFactionGroup `json:"factions"`
	}
	if err := mageloPost("/quests/factions", map[string]any{}, &out); err != nil {
		return nil, err
	}
	if out.Factions == nil {
		out.Factions = []QuestFactionGroup{}
	}
	return out.Factions, nil
}

// ListQuests returns every quest with its steps, rewards and prerequisites.
func (a *App) ListQuests() ([]Quest, error) {
	var out struct {
		Quests []Quest `json:"quests"`
	}
	if err := mageloPost("/quests/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	if out.Quests == nil {
		out.Quests = []Quest{}
	}
	return out.Quests, nil
}

// SaveQuest creates (ID 0) or updates one quest, replacing its steps, rewards
// and prerequisites wholesale. The server resolves every item name against the
// item DB first, so a typo fails the whole save rather than storing a quest
// that prices nothing.
func (a *App) SaveQuest(q Quest) error {
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := mageloPost("/quests/save", map[string]any{
		"id": q.ID, "name": q.Name, "class": q.Class, "wiki_url": q.Wiki,
		"prereqs": q.Prereqs, "rewards": q.Rewards, "steps": q.Steps,
	}, &out); err != nil {
		return err
	}
	if out.Error != "" {
		// Server validation messages ("Step 3: X is not in the item DB") are
		// written to be read by the officer editing, so they pass through.
		return fmt.Errorf("%s", out.Error)
	}
	return nil
}

// DeleteQuest removes one quest; its steps, rewards and prerequisite links go
// with it, including rows where it was somebody else's prerequisite.
func (a *App) DeleteQuest(id int) error {
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := mageloPost("/quests/delete", map[string]any{"id": id}, &out); err != nil {
		return err
	}
	if out.Error != "" {
		return fmt.Errorf("%s", out.Error)
	}
	return nil
}

// SearchQuestMobs backs the NPC autocomplete. Fewer than two characters
// returns the known quest-NPC roster instead of nothing, so opening the picker
// is already useful before anything is typed.
func (a *App) SearchQuestMobs(q string) ([]QuestMob, error) {
	var out struct {
		Mobs []QuestMob `json:"mobs"`
	}
	if err := mageloPost("/quests/mobs/search", map[string]any{"q": q}, &out); err != nil {
		return nil, err
	}
	if out.Mobs == nil {
		out.Mobs = []QuestMob{}
	}
	return out.Mobs, nil
}

// ListQuestZones returns every zone, for the picker on a ground-spawn step. The
// list is small and effectively static, so it's fetched whole and filtered in
// the UI rather than round-tripping per keystroke.
func (a *App) ListQuestZones() ([]QuestZone, error) {
	var out struct {
		Zones []QuestZone `json:"zones"`
	}
	if err := mageloPost("/quests/zones", map[string]any{}, &out); err != nil {
		return nil, err
	}
	if out.Zones == nil {
		out.Zones = []QuestZone{}
	}
	return out.Zones, nil
}

// QuestMobSaveResult is what a save attempt came back with. Exactly one of
// Duplicate and Mob is meaningful: a non-empty Duplicate means nothing was
// written and the caller must ask before retrying with confirm.
type QuestMobSaveResult struct {
	Mob QuestMob `json:"mob"`
	// Duplicate is set when a mob of the same name already exists. This is a
	// question, not an error — EQ genuinely reuses NPC names, and the class
	// epics depend on telling those NPCs apart (mad vs sane Kaiaren, the two
	// Spirit Sentinels). Mob carries the existing row so the caller can show
	// where it is.
	Duplicate string `json:"duplicate"`
}

// SaveQuestMob creates (ID 0) or updates one NPC and returns the stored row —
// the zone name is resolved by the server, not echoed back from what was sent,
// so the caller shows what was actually written.
//
// confirm re-submits past the duplicate-name question. Pass false first and
// only pass true once the officer has actually been asked.
func (a *App) SaveQuestMob(m QuestMob, confirm bool) (QuestMobSaveResult, error) {
	var out struct {
		Mob       QuestMob `json:"mob"`
		Duplicate string   `json:"duplicate"`
		Error     string   `json:"error"`
	}
	if err := mageloPost("/quests/mobs/save", map[string]any{
		"id": m.ID, "name": m.Name, "nicknames": m.Nicknames,
		"zone_id": m.ZoneID, "faction": m.Faction, "quest_mob": m.QuestMob,
		"confirm": confirm,
	}, &out); err != nil {
		return QuestMobSaveResult{}, err
	}
	if out.Error != "" {
		// Validation messages are written for the officer editing; pass through.
		return QuestMobSaveResult{}, fmt.Errorf("%s", out.Error)
	}
	return QuestMobSaveResult{Mob: out.Mob, Duplicate: out.Duplicate}, nil
}

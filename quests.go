package main

import "fmt"

// Quest turn-in recipes: hand in a set of component items, receive a reward
// item. Reading the list needs a linked client; creating and editing is
// officer-only and enforced server-side — the admin-mode buttons in the Magelo
// view only decide whether to show the controls, never whether the save lands.

// QuestComponent mirrors the server's component record: one item handed in,
// priced so a reward that was never auctioned can still be valued by what it
// cost. Value is what it contributes — its own median, or, when the component
// is itself the reward of an earlier quest in a turn-in chain, that quest's
// total. FromQuest distinguishes the two.
type QuestComponent struct {
	// Name repeats when a step wants more than one — hand-ins take unstacked
	// items, so two Bottles of Milk are two entries, not a quantity.
	Name string `json:"name"`
	// Consumed is false for an item handed over and given straight back, which
	// costs nothing and so has Value 0.
	Consumed  bool `json:"consumed"`
	DkpCount  int  `json:"dkp_count,omitempty"`
	DkpMedian int  `json:"dkp_median,omitempty"`
	Value     int  `json:"value,omitempty"`
	FromQuest bool `json:"from_quest,omitempty"`
	// Free marks a component whose own quest demands nothing — a dialogue
	// grant, a ground spawn. Separates "known to cost nothing" from "we have no
	// price", which are both a zero.
	Free bool `json:"free,omitempty"`
}

// QuestTurnIn is one component as the editor stores it: one trade slot. A step
// wanting two of something sends two of these.
type QuestTurnIn struct {
	Name     string `json:"name"`
	Consumed bool   `json:"consumed"`
}

// Quest mirrors the server's recipe record. Rewards and Items are plain item
// names in the order the officer entered them; ID is 0 for a recipe that
// hasn't been saved yet. A quest can hand back several items, and its rewards
// are routinely the turn-ins of the next quest in a chain.
type Quest struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Faction is the minimum standing required, as the EQ con word ("Ally",
	// "Kindly", …), and FactionGroup is who it's with ("Coldain"). Both empty
	// means no faction gate; the server only ever stores them together. The
	// editor's level dropdown mirrors the server's questFactions list — both
	// must agree or a save is rejected — while the group is free text.
	Faction      string `json:"faction"`
	FactionGroup string `json:"faction_group"`
	// MobID is the eqmobs row the turn-ins are handed to, 0 for none. MobName
	// comes back resolved for display and is ignored on save — mob names aren't
	// unique in eqmobs, so the id is the only safe reference.
	MobID   int    `json:"mob_id"`
	MobName string `json:"mob_name"`
	// Class restricts the step to one EQ class, "" for anyone. StepKind is how
	// the item is obtained — a hand-in, a combine, a kill, a ground spawn, or
	// dialogue. PlatCost is coin demanded alongside the items and is
	// deliberately kept out of the DKP total.
	Class    string        `json:"class"`
	StepKind string        `json:"step_kind"`
	PlatCost int           `json:"plat_cost"`
	Rewards  []string      `json:"rewards"`
	Items    []QuestTurnIn `json:"items"`
}

// QuestMob is one hand-in NPC: an eqmobs row, with the two columns a turn-in
// NPC needs on top of what every mob carries. ZoneName is resolved from ZoneID
// server-side for display; ZoneID is what's stored.
type QuestMob struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Nicknames string `json:"nicknames"`
	ZoneID    string `json:"zone_id"`
	ZoneName  string `json:"zone_name"`
	Faction   string `json:"faction"`
	// QuestMob marks the mob as a turn-in NPC, which floats it to the top of
	// the editor's search. Attaching a mob to a quest sets it server-side.
	QuestMob bool `json:"quest_mob"`
}

// QuestZone is one entry in the zone picker on the add-NPC form.
type QuestZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// QuestFactionGroup is one autocomplete suggestion for the "with" field.
// Quests is how many recipes currently use it — the list arrives ordered by
// that (most-used first), then alphabetically.
type QuestFactionGroup struct {
	Name   string `json:"name"`
	Quests int    `json:"quests"`
}

// ListQuestFactions returns the faction-group autocomplete list: a seed roster
// from the P99 faction guide merged with every group already in use, ordered
// so the guild's own factions come first.
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

// ListQuests returns every turn-in recipe, for the admin editor and for
// anything that wants the catalog without going item by item.
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

// SaveQuest creates (ID 0) or updates one recipe, replacing both its reward
// and turn-in lists wholesale. The server resolves every name against the item
// DB first, so a typo fails the whole save rather than storing a recipe that
// prices nothing.
func (a *App) SaveQuest(q Quest) error {
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := mageloPost("/quests/save", map[string]any{
		"id": q.ID, "name": q.Name,
		"faction": q.Faction, "faction_group": q.FactionGroup,
		"mob_id": q.MobID, "class": q.Class, "step_kind": q.StepKind,
		"plat_cost": q.PlatCost,
		"rewards":   q.Rewards, "items": q.Items,
	}, &out); err != nil {
		return err
	}
	if out.Error != "" {
		// Server validation messages ("X is not in the item DB") are written to
		// be read by the officer editing, so they pass through verbatim.
		return fmt.Errorf("%s", out.Error)
	}
	return nil
}

// SearchQuestMobs backs the hand-in NPC autocomplete. Fewer than two
// characters returns the known turn-in roster instead of nothing, so opening
// the picker is already useful before anything is typed.
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

// ListQuestZones returns every zone, for the picker on the add-NPC form. The
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

// SaveQuestMob creates (ID 0) or updates one hand-in NPC and returns the stored
// row — the zone name is resolved by the server, not echoed back from what was
// sent, so the caller shows what was actually written.
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

// DeleteQuest removes one recipe; its turn-in rows go with it.
func (a *App) DeleteQuest(id int) error {
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := mageloPost("/quests/delete", map[string]any{"id": id}, &out); err != nil {
		return err
	}
	if out.Error != "" {
		// Server validation messages ("X is not in the item DB") are written to
		// be read by the officer editing, so they pass through verbatim.
		return fmt.Errorf("%s", out.Error)
	}
	return nil
}

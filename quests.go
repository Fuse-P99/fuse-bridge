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
	Name      string `json:"name"`
	DkpCount  int    `json:"dkp_count,omitempty"`
	DkpMedian int    `json:"dkp_median,omitempty"`
	Value     int    `json:"value,omitempty"`
	FromQuest bool   `json:"from_quest,omitempty"`
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
	Faction      string   `json:"faction"`
	FactionGroup string   `json:"faction_group"`
	Rewards      []string `json:"rewards"`
	Items        []string `json:"items"`
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
		"rewards": q.Rewards, "items": q.Items,
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

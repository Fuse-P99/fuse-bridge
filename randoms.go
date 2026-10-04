package main

// /random tracking for the Randoms overlay.
//
// EQ splits one roll across two consecutive lines — the roller's name is on the
// first, the number on the second:
//
//	**A Magic Die is rolled by Bitti.
//	**It could have been any number from 0 to 555, but this time it turned up a 18.
//
// So the name line is held as "pending" until its result arrives. The game emits
// the pair back-to-back for a given roll; randomPairWindow bounds how long an
// orphaned name line (client hiccup, truncated log) can sit around waiting to be
// mis-attributed to someone else's roll.
//
// Rolls are grouped by their range, because that's the question being asked at
// loot time: everyone rolling on one item uses the same /random N, and a second
// item in flight is a second range. Ranges starting at 0 (very nearly all of
// them — /random N implies 0) are labeled by their maximum alone.
//
// The range is also how the item gets named. Whoever is running the loot names
// the item and the number together ("Cloak of Flames 555") — but in whichever
// channel the raid happens to be using: guild, raid, group, say, ooc, auction,
// shout, or a tell. Rather than enumerate channels, every quoted chat line is
// kept for randomHintLookback and searched when a new range opens. The roll
// number is distinctive enough to do the filtering on its own; the rest of the
// matching message, minus the call-out filler, is the item name.
//
// Purely local: /random is broadcast to everyone in range, so the player's own
// log already holds every roll. Nothing is sent to or read from the server, and
// nothing persists — a roll is only interesting for as long as the loot is being
// decided (randomRollTTL).

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	randomPairWindow = 10 * time.Second // max gap between the name and result lines
	randomMaxGroups  = 8                // bound a night of rolling
	randomMaxRolls   = 80               // per group; a full raid can all roll on one item

	// A roll-off's life after its last roll: the full ranked list stands for
	// randomCollapseAfter, then folds to a one-line result, then goes away at
	// randomGroupTTL. Both are measured from the last roll, so a straggler
	// re-opens the list rather than extending a collapsed summary.
	randomCollapseAfter = 30 * time.Second
	randomGroupTTL      = 5 * time.Minute

	// How far back a call-out can sit and still name a roll. Long enough for
	// "Cloak of Flames 555" → questions → rolling, short enough that last hour's
	// 555 doesn't claim this one.
	randomHintLookback = 5 * time.Minute
	// Grace the other way: the call-out sometimes lands just after the first
	// eager roll. Only an unnamed, brand-new group can be claimed this way.
	randomHintLate = 45 * time.Second
	// Every channel feeds this ring, so it has to survive a busy zone's ooc and
	// auction traffic without dropping the raid line that matters.
	randomMaxHints = 150
	randomItemMax  = 48 // display cap on a parsed item name
)

var (
	// Names are letters plus the apostrophe/backtick some surnames carry. The
	// value line's range is captured so /random 100 500 (a non-zero floor) keeps
	// its full label instead of collapsing into the 0-500 group.
	randomRollerRE = regexp.MustCompile("^\\*\\*A Magic Die is rolled by ([A-Za-z`'-]+)\\.$")
	randomResultRE = regexp.MustCompile(`^\*\*It could have been any number from (\d+) to (\d+), but this time it turned up a (\d+)\.$`)
	// Any quoted chat line, whatever the channel. EQ writes every one of them as
	// "<who does what>, '<message>'" — "Bitti tells the guild,", "You say,",
	// "Bitti says out of character,", "Bitti tells you,", and so on. Matching the
	// shape instead of the channel list means a raid calling loot in /ooc or a
	// tell works with no new pattern. The body is greedy to the final quote so an
	// apostrophe inside the message doesn't truncate it.
	randomChatRE = regexp.MustCompile(`^.{0,60}?, '(.*)'$`)
)

// Words that surround an item in a roll call-out but aren't part of its name.
// Only stripped from the ends of the message, so "Ring of Fire" keeps its "of"
// while "roll on Ring of Fire 555" still resolves cleanly.
var randomFiller = map[string]bool{
	"random": true, "randoms": true, "rando": true,
	"roll": true, "rolls": true, "rolling": true, "rolled": true,
	"for": true, "on": true, "to": true, "the": true, "a": true, "an": true,
	"please": true, "pls": true, "plz": true, "now": true, "everyone": true,
	"all": true, "up": true, "go": true, "next": true, "item": true,
	// Auction/ooc shorthand, since those channels feed the hints too.
	"wts": true, "wtb": true, "wtt": true, "pst": true, "bid": true, "bids": true,
}

// RandomRoll is one player's roll. Sent to the frontend.
type RandomRoll struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	At    int64  `json:"at"` // unix millis

	// Seq numbers a player's rolls on this range in the order they were made,
	// and is 0 for the overwhelmingly common case of rolling exactly once — so
	// the overlay only annotates the players who actually rolled twice.
	Seq int `json:"seq"`
	// Superseded marks every roll after a player's first (Seq >= 2). The raid
	// rule is that your first roll stands, so these are shown struck through and
	// take no part in the ranking or the winner.
	Superseded bool `json:"superseded"`
	// Rank among the rolls that count, ties sharing a place (1, 1, 3). 0 on a
	// superseded roll, which has no standing.
	Rank int `json:"rank"`
}

// RandomGroup collects every roll made on one range, highest first.
type RandomGroup struct {
	Min    int          `json:"min"`
	Max    int          `json:"max"`
	Label  string       `json:"label"` // "555", or "100-500" when the floor isn't 0
	Item   string       `json:"item"`  // from the chat call-out; "" when unmatched
	LastAt int64        `json:"last_at"`
	Rolls  []RandomRoll `json:"rolls"`

	// Collapsed folds the finished roll-off down to its result line. Set on
	// read, since it turns on with the clock rather than with a new roll.
	Collapsed bool `json:"collapsed"`
	// The winning roll, counting only rolls that stand. WinnerName joins every
	// name on a tie — a tie for first is the one case where the summary line
	// must not silently pick someone.
	WinnerName  string `json:"winner_name"`
	WinnerValue int    `json:"winner_value"`

	firstAt int64 // when this range opened; anchors the call-out lookback
}

// randomHint is one recent guild-chat line, kept only long enough to name a
// roll that starts right after it.
type randomHint struct {
	text string
	at   time.Time
}

var (
	randomMu     sync.Mutex
	randomGroups []*RandomGroup
	randomHints  []randomHint
	// The name from the last "is rolled by" line, waiting for its number.
	randomPendingName string
	randomPendingAt   time.Time
)

// randomLabel names a range the way players say it out loud: "/random 555" is
// just "555", and only an explicit floor earns the full span.
func randomLabel(min, max int) string {
	if min == 0 {
		return fmt.Sprintf("%d", max)
	}
	return fmt.Sprintf("%d-%d", min, max)
}

// RecordRandomLine feeds one raw log line to the roll tracker. Called for every
// tailed line from main.
func RecordRandomLine(line string) {
	content := logMessageContent(line)
	// Cheap gates first — this runs on every tailed line. Roll lines both start
	// with "**"; chat of any channel is the only other shape of interest, and
	// every channel writes ", '" ahead of the message.
	isRoll := len(content) >= 2 && content[0] == '*' && content[1] == '*'
	isChat := !isRoll && strings.Contains(content, ", '") && strings.HasSuffix(content, "'")
	if !isRoll && !isChat {
		return
	}
	// The log's own timestamp, not the wall clock: at startup the tailer can
	// replay a batch of lines written before the app opened, and those rolls
	// should age from when they actually happened (usually straight into the
	// TTL prune) rather than all landing on "now".
	at := logLineTime(line)
	if at.IsZero() {
		at = time.Now()
	}

	if isChat {
		noteRandomHint(content, at)
		return
	}

	if m := randomRollerRE.FindStringSubmatch(content); m != nil {
		randomMu.Lock()
		randomPendingName = m[1]
		randomPendingAt = at
		randomMu.Unlock()
		return
	}

	m := randomResultRE.FindStringSubmatch(content)
	if m == nil {
		return
	}
	// The regex already guaranteed digits; Atoi can only fail here on a number
	// too large for an int, which no roll produces.
	min, _ := strconv.Atoi(m[1])
	max, _ := strconv.Atoi(m[2])
	val, _ := strconv.Atoi(m[3])
	if max <= min {
		return
	}

	randomMu.Lock()
	name := randomPendingName
	// An unclaimed name line older than the pair window belongs to a roll whose
	// result never arrived; attributing this number to it would be a lie.
	if name == "" || at.Sub(randomPendingAt) > randomPairWindow || at.Before(randomPendingAt) {
		randomMu.Unlock()
		return
	}
	randomPendingName = ""
	addRandomRollLocked(min, max, name, val, at.UnixMilli())
	randomMu.Unlock()

	emitRandomsChanged()
}

// noteRandomHint files a chat line as a possible roll call-out, and lets it name
// a range that just opened without one (the call sometimes lands a beat after
// the first eager roll).
func noteRandomHint(content string, at time.Time) {
	m := randomChatRE.FindStringSubmatch(content)
	if m == nil {
		return
	}
	text := strings.TrimSpace(m[1])
	if text == "" {
		return
	}

	randomMu.Lock()
	randomHints = append(randomHints, randomHint{text: text, at: at})
	cutoff := at.Add(-randomHintLookback)
	kept := randomHints[:0]
	for _, h := range randomHints {
		if h.at.After(cutoff) {
			kept = append(kept, h)
		}
	}
	randomHints = kept
	if len(randomHints) > randomMaxHints {
		randomHints = randomHints[len(randomHints)-randomMaxHints:]
	}

	changed := false
	for _, g := range randomGroups {
		if g.Item != "" || at.Sub(time.UnixMilli(g.firstAt)) > randomHintLate {
			continue
		}
		if item := randomItemFrom(text, g.Max); item != "" {
			g.Item = item
			changed = true
		}
	}
	randomMu.Unlock()

	if changed {
		emitRandomsChanged()
	}
}

// randomItemFrom pulls an item name out of a call-out that mentions max as a
// standalone number. Returns "" when the line isn't about this roll, or when
// nothing recognizable survives stripping the filler.
func randomItemFrom(text string, max int) string {
	want := strconv.Itoa(max)
	fields := strings.Fields(text)
	rest := make([]string, 0, len(fields))
	found := false
	for _, f := range fields {
		// Compare on the bare digits so "555," "(555)" and "555." all count,
		// while "1555" and "55" never do.
		if strings.Trim(f, "()[]{}<>,.:;!?-/\\'\"") == want {
			found = true
			continue
		}
		rest = append(rest, f)
	}
	if !found {
		return ""
	}

	// Filler only comes off the ends: an item can legitimately contain "of",
	// "the" or "on" in the middle of its name.
	lower := func(s string) string {
		return strings.ToLower(strings.Trim(s, "()[]{}<>,.:;!?-/\\'\""))
	}
	for len(rest) > 0 && randomFiller[lower(rest[0])] {
		rest = rest[1:]
	}
	for len(rest) > 0 && randomFiller[lower(rest[len(rest)-1])] {
		rest = rest[:len(rest)-1]
	}

	item := strings.TrimSpace(strings.Join(rest, " "))
	item = strings.Trim(item, "-–—:;,./\\|*")
	item = strings.TrimSpace(item)
	// Guard against naming a roll after a stray number in unrelated chat: a real
	// item name has letters and more than a couple of them.
	if len([]rune(item)) < 3 || !strings.ContainsFunc(item, isRandomLetter) {
		return ""
	}
	if r := []rune(item); len(r) > randomItemMax {
		item = strings.TrimSpace(string(r[:randomItemMax])) + "…"
	}
	return item
}

func isRandomLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// randomItemForLocked searches the kept chat lines, newest first, for the
// call-out that named this range: the number narrows the field, and the first
// candidate that yields a usable item name wins. Caller holds randomMu.
func randomItemForLocked(max int, firstAt time.Time) string {
	oldest := firstAt.Add(-randomHintLookback)
	for i := len(randomHints) - 1; i >= 0; i-- {
		h := randomHints[i]
		// Only lines before the roll: a later line is a different call-out, and
		// noteRandomHint handles the narrow late-arrival case on its own.
		if h.at.After(firstAt) || h.at.Before(oldest) {
			continue
		}
		if item := randomItemFrom(h.text, max); item != "" {
			return item
		}
	}
	return ""
}

// addRandomRollLocked files a roll under its range, creating the group if this
// is the first roll on it. Caller holds randomMu.
func addRandomRollLocked(min, max int, name string, val int, atMs int64) {
	var g *RandomGroup
	for _, cand := range randomGroups {
		if cand.Min == min && cand.Max == max {
			g = cand
			break
		}
	}
	if g == nil {
		g = &RandomGroup{
			Min: min, Max: max, Label: randomLabel(min, max), firstAt: atMs,
		}
		g.Item = randomItemForLocked(max, time.UnixMilli(atMs))
		randomGroups = append(randomGroups, g)
	}

	// Every roll is kept, including a second one by the same player. Collapsing
	// those would hide a re-roll, which is exactly the thing an officer needs to
	// see when deciding who won.
	g.Rolls = append(g.Rolls, RandomRoll{Name: name, Value: val, At: atMs})
	if len(g.Rolls) > randomMaxRolls {
		g.Rolls = g.Rolls[len(g.Rolls)-randomMaxRolls:]
	}
	if atMs > g.LastAt {
		g.LastAt = atMs
	}
	pruneRandomsLocked()
}

// pruneRandomsLocked drops finished roll-offs and the oldest groups beyond the
// cap. Caller holds randomMu.
//
// Whole groups age out together on their last roll, rather than roll by roll:
// the collapsed summary has to name a winner, and it can't do that from a list
// that's been eaten from the bottom.
func pruneRandomsLocked() {
	cutoff := time.Now().Add(-randomGroupTTL).UnixMilli()
	kept := randomGroups[:0]
	for _, g := range randomGroups {
		if len(g.Rolls) == 0 || g.LastAt <= cutoff {
			continue
		}
		kept = append(kept, g)
	}
	randomGroups = kept

	if len(randomGroups) > randomMaxGroups {
		sort.SliceStable(randomGroups, func(i, j int) bool {
			return randomGroups[i].LastAt > randomGroups[j].LastAt
		})
		randomGroups = randomGroups[:randomMaxGroups]
	}
}

// GetRandomRolls returns the live roll groups: newest activity first, and within
// each group the highest roll first (ties broken by who rolled first), annotated
// with duplicate sequence numbers, ranks and the winner. Bound to the frontend;
// the overlay polls it.
//
// All of that is derived here rather than stored, because it's a pure function
// of the rolls plus the clock — and the clock is what flips a group to its
// collapsed summary with no new log line to trigger a recompute.
func (a *App) GetRandomRolls() []RandomGroup {
	randomMu.Lock()
	defer randomMu.Unlock()

	// Prune on read too, or a group would sit on the overlay forever once the
	// rolling stopped — there'd be no further line to trigger the cleanup.
	pruneRandomsLocked()

	now := time.Now().UnixMilli()
	out := make([]RandomGroup, 0, len(randomGroups))
	for _, g := range randomGroups {
		rolls := make([]RandomRoll, len(g.Rolls))
		copy(rolls, g.Rolls)

		// Pass one, in roll order: number each repeat roller's attempts. A player
		// who rolled once is left at Seq 0 and never annotated.
		total := map[string]int{}
		for _, r := range rolls {
			total[randomNameKey(r.Name)]++
		}
		seen := map[string]int{}
		for i := range rolls {
			k := randomNameKey(rolls[i].Name)
			seen[k]++
			if total[k] > 1 {
				rolls[i].Seq = seen[k]
				rolls[i].Superseded = seen[k] > 1
			}
		}

		// Pass two: order by value, then rank the rolls that stand. Superseded
		// rolls keep their place in the list — seeing where a discarded 998 fell
		// is the point of showing it — but take no rank.
		sort.SliceStable(rolls, func(i, j int) bool {
			if rolls[i].Value != rolls[j].Value {
				return rolls[i].Value > rolls[j].Value
			}
			return rolls[i].At < rolls[j].At
		})
		place, prev, counted := 0, -1, 0
		var winners []string
		winVal := 0
		for i := range rolls {
			if rolls[i].Superseded {
				continue
			}
			counted++
			if rolls[i].Value != prev {
				place = counted
				prev = rolls[i].Value
			}
			rolls[i].Rank = place
			if place == 1 {
				winners = append(winners, rolls[i].Name)
				winVal = rolls[i].Value
			}
		}

		out = append(out, RandomGroup{
			Min: g.Min, Max: g.Max, Label: g.Label, Item: g.Item,
			LastAt: g.LastAt, Rolls: rolls,
			Collapsed:   now-g.LastAt > randomCollapseAfter.Milliseconds(),
			WinnerName:  strings.Join(winners, " / "),
			WinnerValue: winVal,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastAt > out[j].LastAt })
	return out
}

// randomNameKey matches rolls to a player. EQ is consistent about capitalizing
// names, but folding costs nothing and a mismatch would silently split someone's
// two rolls into two separate rollers.
func randomNameKey(name string) string { return strings.ToLower(name) }

// ClearRandomRolls empties the overlay on demand — for when a roll-off is
// settled and the next one shouldn't be read against stale numbers. Bound to
// the frontend.
func (a *App) ClearRandomRolls() {
	randomMu.Lock()
	randomGroups = nil
	randomHints = nil
	randomPendingName = ""
	randomMu.Unlock()
	emitRandomsChanged()
}

func emitRandomsChanged() {
	if v3App != nil {
		v3App.Event.Emit("randoms-changed")
	}
}

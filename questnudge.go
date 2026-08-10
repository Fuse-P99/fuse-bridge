package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Zone-entry quest nudges: walking into a zone that holds something a tracked
// quest still needs puts ONE temporary marker on the map (at the known spot,
// else the zone's center) and one line in the status feed. One, not one per
// need — several flags stacked on a map obscure each other into unreadability,
// so the next thing to do gets the flag and the status line lists the rest.
// Only steps the character can actually DO count (questStepFeasible): a
// hand-in isn't a reminder when the components aren't in the bags.
// SetCurrentZone fires on every /who footer too, so the nudge dedupes on an
// actual (character, zone) change rather than on being called.

var (
	nudgeMu       sync.Mutex
	lastNudgeZone string
	lastNudgeChar string
)

// ── app-wide banner ─────────────────────────────────────────────────────────
// The nudge's notification path mirrors batphones: App.svelte polls a bound
// method on the same timer and renders a top-of-app bar — blue, to match the
// temp quest marker it points at. Local-only (nothing touches the server) and
// transient: it ages out on its own, and a zone change clears it at once so
// the bar never describes a zone the player already left.

type QuestNudgeBanner struct {
	Text   string `json:"text"`
	SentAt int64  `json:"sent_at"`
}

const questNudgeBannerTTL = 90 * time.Second

var (
	nudgeBannerMu sync.Mutex
	nudgeBanner   QuestNudgeBanner
)

func setQuestNudgeBanner(text string) {
	nudgeBannerMu.Lock()
	nudgeBanner = QuestNudgeBanner{Text: text, SentAt: time.Now().UnixMilli()}
	nudgeBannerMu.Unlock()
}

func clearQuestNudgeBanner() {
	nudgeBannerMu.Lock()
	nudgeBanner = QuestNudgeBanner{}
	nudgeBannerMu.Unlock()
}

// GetQuestNudge returns the live zone-entry reminder, nil once aged out.
func (a *App) GetQuestNudge() *QuestNudgeBanner {
	nudgeBannerMu.Lock()
	defer nudgeBannerMu.Unlock()
	if nudgeBanner.Text == "" ||
		time.Since(time.UnixMilli(nudgeBanner.SentAt)) > questNudgeBannerTTL {
		return nil
	}
	b := nudgeBanner
	return &b
}

func questTrackZoneChange(zone string) {
	toon := currentCharName
	if toon == "" || strings.TrimSpace(zone) == "" {
		return
	}
	nudgeMu.Lock()
	if strings.EqualFold(lastNudgeZone, zone) && strings.EqualFold(lastNudgeChar, toon) {
		nudgeMu.Unlock()
		return
	}
	lastNudgeZone, lastNudgeChar = zone, toon
	nudgeMu.Unlock()
	// The previous zone's reminder is stale the moment the zone changes;
	// questNudge re-sets the banner if the new zone owes anything.
	clearQuestNudgeBanner()
	// Off the tail loop: the nudge reads state and writes markers.
	go questNudge(toon, zone)
}

// questStepFeasible reports whether the character can actually ACT on a step
// right now, as far as the evidence shows. Two gates:
//
//   - A `follows` step only exists downstream of its predecessor (hand in the
//     head, a spirit spawns, kill it): with the previous step not done there
//     is nothing in the zone to act on yet.
//   - A hand-in or combine consumes its inputs, so flagging one without the
//     components in the bags is exactly the false promise the nudge must not
//     make. Judged only when inventory evidence exists (a scanned dump, or
//     loot observations); with no evidence at all the nudge stands — going
//     silent for every character who never ran /outputfile would gut the
//     reminder, and an optimistic flag is honest when nothing contradicts it.
//
// The VP medallion hand-ins are judged by piece IDENTITY instead of the item
// name: the nine pieces share one name, so "holds three pieces" means nothing
// unless they are this rune's three.
func questStepFeasible(toon string, q *Quest, idx int) bool {
	if idx < 0 || idx >= len(q.Steps) {
		return false
	}
	s := &q.Steps[idx]
	if s.Follows && idx > 0 && !tqStepDone(toon, q.ID, idx-1) {
		return false
	}
	if s.Kind != "handin" && s.Kind != "combine" {
		return true
	}
	held := tqHeldCounts(toon)
	if len(held) == 0 && tqInvModMs(toon) == 0 {
		return true // no evidence either way — can't judge
	}
	if ids := questStepRunePieces(q, idx); ids != nil {
		have := tqHeldMedallionIDs(toon)
		for _, id := range ids {
			if !have[id] {
				return false
			}
		}
		return true
	}
	// Roll the input slots the way the walkthrough renders them: identical
	// requirements stack into one slot × N, and any alternative satisfies its
	// slot. A slot is covered when the held counts across its alternatives
	// reach the stack size.
	type slot struct {
		names []string
		n     int
	}
	slots := map[string]*slot{}
	for ii := range s.Items {
		it := &s.Items[ii]
		if it.Role == "out" {
			continue
		}
		names := []string{normalizeItemName(it.Name)}
		for _, a := range it.Alts {
			names = append(names, normalizeItemName(a))
		}
		key := strings.Join(names, "|")
		if sl, ok := slots[key]; ok {
			sl.n++
		} else {
			slots[key] = &slot{names: names, n: 1}
		}
	}
	for _, sl := range slots {
		total := 0
		for _, nm := range sl.names {
			total += held[nm]
		}
		if total < sl.n {
			return false
		}
	}
	return true
}

// questStepRunePieces returns the piece ids a medallion-assembly hand-in
// needs (its output is one of the nine-piece runes), or nil for any other
// step.
func questStepRunePieces(q *Quest, idx int) []int {
	s := &q.Steps[idx]
	for ii := range s.Items {
		if s.Items[ii].Role != "out" {
			continue
		}
		if ids := vpPieceIDsForRune(s.Items[ii].Name); ids != nil {
			return ids
		}
	}
	return nil
}

// questNudge collects what this zone still owes the character's quests.
func questNudge(toon, zone string) {
	live := map[int]bool{}
	for _, id := range tqAssignedQuestIDs(toon) {
		live[id] = true
	}
	if len(live) == 0 {
		return
	}

	type nudgeHit struct {
		label  string // marker label
		need   string // what the status line says is here
		y, x   float64
		center bool
	}
	var hits []nudgeHit
	seen := map[string]bool{}

	// Steps of assigned quests located here, not yet done, and actually
	// doable — a hand-in whose components aren't in the bags, or a kill gated
	// behind an unfinished trigger step, is noise here, not a reminder.
	for _, ref := range qdZoneRefs(zone) {
		if !live[ref.QuestID] || tqStepDone(toon, ref.QuestID, ref.Step) {
			continue
		}
		q, ok := questByID(ref.QuestID)
		if !ok || ref.Step >= len(q.Steps) {
			continue
		}
		if !questStepFeasible(toon, &q, ref.Step) {
			continue
		}
		s := &q.Steps[ref.Step]
		// What the step yields here — the thing worth grabbing.
		need := ""
		for ii := range s.Items {
			if s.Items[ii].Role == "out" {
				need = s.Items[ii].Name
				break
			}
		}
		if need == "" {
			need = s.Kind
		}
		key := strings.ToLower(q.Name + "|" + need)
		if seen[key] {
			continue
		}
		seen[key] = true

		h := nudgeHit{label: q.Name + ": " + need, need: need, center: true}
		// The marker's spot, most specific first: the step's own loc, a lone
		// located mob, else the zone center. Axis order matches the editor's
		// dropMarker — /loc Y then X.
		if s.HasLoc {
			h.y, h.x, h.center = float64(s.LocY), float64(s.LocX), false
		} else {
			var located *QuestStepMob
			inZone := 0
			for mi := range s.Mobs {
				if normalizeZoneKey(s.Mobs[mi].Zone) != normalizeZoneKey(zone) {
					continue
				}
				inZone++
				if s.Mobs[mi].HasLoc {
					located = &s.Mobs[mi]
				}
			}
			if inZone == 1 && located != nil {
				h.y, h.x, h.center = float64(located.LocY), float64(located.LocX), false
			}
		}
		hits = append(hits, h)
	}

	// Medallion pieces: when a medallion quest is on the books and this zone
	// is a missing piece's source, point at it — id-accurate because each
	// piece has its own zone.
	medallionQuest := false
	for id := range live {
		if q, ok := questByID(id); ok && questUsesMedallions(&q) {
			medallionQuest = true
			break
		}
	}
	if medallionQuest {
		if m := vpMedallionByZone(zone); m != nil && !tqHeldMedallionIDs(toon)[m.ID] {
			h := nudgeHit{
				label:  fmt.Sprintf("%s (%s piece)", m.Rune, m.Piece),
				need:   fmt.Sprintf("the %s piece of the %s — %s", m.Piece, m.Rune, m.Source),
				center: !m.HasLoc,
			}
			if m.HasLoc {
				h.y, h.x = m.LocY, m.LocX
			}
			hits = append(hits, h)
		}
	}

	if len(hits) == 0 {
		return
	}
	// The single marker goes on the next thing to do — the first hit, which
	// is the earliest not-done step of the first assigned quest with business
	// here (medallion pieces come last). Everything else lives in the status
	// line, where a list is readable.
	next := hits[0]
	addQuestMarkerAuto(zone, next.y, next.x, next.label, next.center)
	needs := make([]string, 0, len(hits))
	for _, h := range hits {
		needs = append(needs, h.need)
	}
	note := "marked on the map"
	if len(hits) > 1 {
		note = fmt.Sprintf("%s is marked on the map", next.need)
	}
	msg := fmt.Sprintf("Quest reminder: %s here in %s — %s.",
		strings.Join(needs, "; "), zone, note)
	addStatus("%s", msg)
	setQuestNudgeBanner(msg)
}

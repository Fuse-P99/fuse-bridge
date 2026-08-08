package main

import (
	"fmt"
	"strings"
	"sync"
)

// Zone-entry quest nudges: walking into a zone that holds something a tracked
// quest still needs puts a temporary marker on the map (at the known spot,
// else the zone's center) and one line in the status feed. SetCurrentZone
// fires on every /who footer too, so the nudge dedupes on an actual
// (character, zone) change rather than on being called.

var (
	nudgeMu       sync.Mutex
	lastNudgeZone string
	lastNudgeChar string
)

const questNudgeMaxMarkers = 3

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
	// Off the tail loop: the nudge reads state and writes markers.
	go questNudge(toon, zone)
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

	// Steps of assigned quests located here and not yet done.
	for _, ref := range qdZoneRefs(zone) {
		if !live[ref.QuestID] || tqStepDone(toon, ref.QuestID, ref.Step) {
			continue
		}
		q, ok := questByID(ref.QuestID)
		if !ok || ref.Step >= len(q.Steps) {
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
	shown := hits
	if len(shown) > questNudgeMaxMarkers {
		shown = shown[:questNudgeMaxMarkers]
	}
	needs := make([]string, 0, len(shown))
	for _, h := range shown {
		addQuestMarkerAuto(zone, h.y, h.x, h.label, h.center)
		needs = append(needs, h.need)
	}
	extra := ""
	if n := len(hits) - len(shown); n > 0 {
		extra = fmt.Sprintf(" (+%d more)", n)
	}
	addStatus("Quest reminder: %s here in %s%s — markers are on the map.",
		strings.Join(needs, "; "), zone, extra)
}

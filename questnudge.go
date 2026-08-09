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

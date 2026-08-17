package main

import (
	"strings"
	"sync"
)

// Epic auto-assignment: every character played on this machine gets their
// class's epic quest assigned, once. "Played on this machine" is the log-file
// roster (logFileCharNames), never the /who zone cache full of strangers.
// Idempotence lives in the store: an existing assignment is a no-op, and a
// tombstone (the player removed the epic) is respected forever — the server
// enforces the same rule against a forgetful client.
//
// Class can resolve late (a brand-new alt the roster hasn't classed yet), so
// this runs at startup, on character swap, and again whenever the trigger
// system resolves a class — the same shape as popout class inheritance.

var questAutoMu sync.Mutex

func autoAssignEpics() {
	questAutoMu.Lock()
	defer questAutoMu.Unlock()
	eqDir := GetSettings().EQDirectory
	names := logFileCharNames(eqDir)
	if len(names) == 0 {
		return
	}
	infos := cachedCharInfos(names)
	for _, n := range names {
		class := strings.TrimSpace(infos[strings.ToLower(n)].Class)
		if class == "" {
			continue // unknown class assigns nothing, never guesses
		}
		qid := epicQuestForClass(class)
		if qid == 0 {
			continue
		}
		// An epic can only ever be done once, so a character holding their
		// epic item (a recorded completion) has nothing left to track: a live
		// assignment is retired, and a missing one is never added.
		if tqHasCompletion(n, qid) {
			if tqUnassignQuest(n, qid) {
				addStatus("Quests: %s already has their epic — the quest was removed from their list.", n)
			}
			continue
		}
		if tqAssignQuest(n, qid, "auto") {
			addStatus("Quests: %s epic assigned to %s.", class, n)
		}
	}
}

// questTrackInit wires the whole quest-tracking side up at startup: cached
// catalog + state, an opportunistic refresh and sync, the initial inventory
// scans that seed the Completed Quests log, epic auto-assignment, and the
// background pollers.
func questTrackInit(done <-chan struct{}) {
	loadQuestDefs()
	tqMu.Lock()
	tqLoadLocked()
	tqMu.Unlock()

	_ = refreshQuestDefs(false)

	eqDir := GetSettings().EQDirectory
	names := logFileCharNames(eqDir)
	if IsLinked() && len(names) > 0 {
		// Best-effort class fill for toons the local cache hasn't met, then
		// pull server-side state so a second machine starts where this one is.
		(&App{}).RefreshCharInfos(names)
		syncToonQuests(names)
	}
	// After the sync so server-side rows are judged too; before the scans so
	// a retracted completion can re-record cleanly if real evidence exists.
	questPruneStaleCompletions()
	for _, n := range names {
		questScanInventory(n)
	}
	autoAssignEpics()

	go questInvPoller(done)
	go questSyncPoller(done)
}

// questTrackCharSwap runs when the tailer moves to another character's log:
// pull their server state if this session hasn't, rescan their dump, and give
// auto-assignment a chance in case they're brand new.
func questTrackCharSwap(name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	go func() {
		if IsLinked() && !tqToonSynced(name) {
			syncToonQuests([]string{name})
		}
		questScanInventory(name)
		autoAssignEpics()
	}()
}

// questTrackClassResolved runs when a character's class resolves after the
// fact — the moment the epic can finally be matched.
func questTrackClassResolved() {
	go autoAssignEpics()
}

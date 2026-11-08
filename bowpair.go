package main

import "time"

// A bow shot prints twice. The client logs one arrow as a sourceless non-melee
// line and then the ordinary hit line, in the same instant and for the same
// amount (seen 2026-11-01):
//
//	Cazic Thule was hit by non-melee for 535 points of damage.
//	You hit Cazic Thule for 535 points of damage.
//
// Both parsers that count our own damage (raiddps.go, threat.go) read both
// lines, so every arrow counted double. The bare "hit" verb is how bow and
// thrown damage prints — a wielded weapon always names its own verb — so a
// "hit" of ours with a non-melee line for the same target and amount beside
// it is one arrow, not two.
//
// The HIT line is the copy that counts and the non-melee line is the echo,
// never the other way round. Every other client in range sees the arrow as
// "<name> hits <mob> for 535" and files it under our melee; the server
// max-merges melee across clients and takes non-melee from the owner alone,
// so keeping the echo and dropping the hit would have the server add their
// 535 to ours. Locally the hit is also what lands in the Bow/Throw slice of
// the DPS breakdown and earns a swing's hate, neither of which the echo can.
//
// The echo prints first, so by the time the hit arrives its twin has already
// been tallied; the ledger remembers what that tally did, and the hit takes it
// back. The other order is handled too: a hit on the ledger makes the next
// matching echo a no-op.
//
// The window is one log second: the two lines share a stamp or straddle a
// second boundary, and whole-second stamps allow nothing tighter. It is what
// keeps a thrown dagger and a damage-shield tick that happen to agree on an
// amount from cancelling each other — they would have to agree on the second
// as well. One echo vouches for exactly one hit: a match is consumed.
const bowPairWindow = time.Second

// bowLine is one of the two lines an arrow prints, as a parser tallied it.
type bowLine struct {
	nonMelee bool
	target   string // normThreatMob of what the damage landed on
	dmg      int
	at       time.Time // log time
	// undo takes back what tallying this line did, for an echo whose hit
	// turned out to follow. nil on a hit, and on an echo that tallied nothing.
	undo func()
}

// bowLedger holds one parser's last second of bow-shaped lines. Each parser
// owns one and touches it only under its own lock.
type bowLedger struct {
	lines []bowLine
}

// twin removes and returns the newest line of the other kind that names the
// same target and amount within bowPairWindow of at.
func (l *bowLedger) twin(nonMelee bool, target string, dmg int, at time.Time) (bowLine, bool) {
	l.prune(at)
	for i := len(l.lines) - 1; i >= 0; i-- {
		e := l.lines[i]
		if e.nonMelee != nonMelee && e.dmg == dmg && e.target == target {
			l.lines = append(l.lines[:i], l.lines[i+1:]...)
			return e, true
		}
	}
	return bowLine{}, false
}

// note remembers a line that was tallied, so its twin can recognise it.
func (l *bowLedger) note(e bowLine) {
	l.prune(e.at)
	l.lines = append(l.lines, e)
}

// prune forgets lines more than bowPairWindow from at, in either direction.
func (l *bowLedger) prune(at time.Time) {
	keep := l.lines[:0]
	for _, e := range l.lines {
		if d := at.Sub(e.at); d <= bowPairWindow && d >= -bowPairWindow {
			keep = append(keep, e)
		}
	}
	// Clear the tail so a dropped line's undo closure can be collected.
	for i := len(keep); i < len(l.lines); i++ {
		l.lines[i] = bowLine{}
	}
	l.lines = keep
}

func (l *bowLedger) reset() { l.lines = nil }

package main

import (
	"strings"
	"sync/atomic"
)

// EQ writes one log per (character, world): eqlog_<Char>_<Token>.txt. The token
// identifies which server the character is on. P99 Blue is the "home" world —
// the only one whose data may ever reach the Fuse bridgekeeper. On every other
// world (P99 Green/Red, the Fuse test server, or any unrecognized EQEmu world)
// the client runs in a reduced, essentially serverless mode: it tails the log
// for LOCAL features (triggers, overlays, timers, quest tracking, log search)
// and a linked client may still DOWNLOAD the Fuse trigger package — but nothing
// about gameplay is ever sent back. This is the single guardrail that keeps a
// Green slain line from setting a Blue TOD, a Green guild-say from reaching the
// Blue guild stream, a Green /loc from painting the Blue map, and so on.
const homeServerToken = "project1999" // P99 Blue

// serverLabels maps a known eqlog world token (case-folded) to a friendly name.
// Unknown tokens fall back to the raw token so a new/other world still gets a
// sensible label rather than nothing.
var serverLabels = map[string]string{
	"project1999": "P99 Blue",
	"p1999green":  "P99 Green",
	"p1999pvp":    "P99 Red",
	"fusetesteq":  "Fuse Test",
}

// currentServerToken is the world token of the log currently being tailed
// (e.g. "project1999"). Empty until a log is attached. Read from several
// goroutines (the sender, the side-channel POSTs), so it is stored atomically.
var currentServerToken atomic.Value // string

func setCurrentServerToken(tok string) { currentServerToken.Store(tok) }

func getCurrentServerToken() string {
	if v, ok := currentServerToken.Load().(string); ok {
		return v
	}
	return ""
}

// onHomeServer reports whether the active log is the home (P99 Blue) world.
// Before any log is attached the token is empty, which is NOT home — the
// guardrail fails closed, so nothing forwards until we have positively
// identified the world as Blue.
func onHomeServer() bool {
	return strings.EqualFold(getCurrentServerToken(), homeServerToken)
}

// serverForwardOK is the gate for every server round-trip that PUSHES
// gameplay-derived data (or associates toons): the client must be linked AND
// on the home world. Pure READS of Blue-guild state — the Fuse trigger
// package, the raid timers board, batphones, the raid mob list, attendance,
// world timers — gate on IsLinked() alone: a linked member sees Blue raid
// status from any world, they just can't feed anything into it from there.
func serverForwardOK() bool { return IsLinked() && onHomeServer() }

// serverTokenFromLog pulls the world token out of an eqlog filename:
// eqlog_<Char>_<Token>.txt -> "<Token>". Empty when the name is not an eqlog
// file or carries no token segment.
func serverTokenFromLog(base string) string {
	s := strings.TrimPrefix(base, "eqlog_")
	if s == base {
		return "" // not an eqlog_ file
	}
	s = strings.TrimSuffix(s, ".txt")
	// eqlog_<Char>_<Token>: the character is the first underscore-delimited
	// segment; everything after it is the token (P99 tokens carry no
	// underscore, but keep the remainder intact just in case one ever does).
	i := strings.Index(s, "_")
	if i < 0 || i+1 >= len(s) {
		return ""
	}
	return s[i+1:]
}

// serverLabel resolves a world token to a friendly name for the UI, falling
// back to the raw token for an unrecognized world.
func serverLabel(tok string) string {
	if tok == "" {
		return ""
	}
	if lbl, ok := serverLabels[strings.ToLower(tok)]; ok {
		return lbl
	}
	return tok
}

// currentServerLabel is the friendly name of the world currently being tailed
// (empty before a log is attached), for status lines and the UI.
func currentServerLabel() string { return serverLabel(getCurrentServerToken()) }

// ServerInfo is the frontend's view of which world the client is on (see
// App.GetCurrentServer). Home == false means the reduced, serverless mode.
type ServerInfo struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	Home  bool   `json:"home"`
}

package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Focus watcher: when Settings.HideOverlaysUnfocused is on, overlays hide
// while the foreground window belongs to any process other than EverQuest or
// FuseBridge itself (interacting with an overlay or this app's own window
// must never hide them). EQ running elevated is not an obstacle: reading the
// foreground window's PID needs no rights at all, and opening the process for
// its image name uses PROCESS_QUERY_LIMITED_INFORMATION, which Windows grants
// against elevated processes from a non-elevated caller.
//
// Fail-open by design: if the foreground process can't be identified (secure
// desktop, UAC prompt, transient window churn) the overlays stay visible —
// wrongly showing beats wrongly vanishing mid-raid.

var (
	fwUser32          = syscall.NewLazyDLL("user32.dll")
	procForegroundWin = fwUser32.NewProc("GetForegroundWindow")
	procWinThreadPID  = fwUser32.NewProc("GetWindowThreadProcessId")
)

// eqFocusExes are the process names counted as "EverQuest is active" —
// the same pair EQ discovery looks for (finder.go).
var eqFocusExes = map[string]bool{"eqgame.exe": true, "everquest.exe": true}

// foregroundProc returns the PID owning the foreground window and its
// lowercase exe basename ("" when it can't be read).
func foregroundProc() (uint32, string) {
	hwnd, _, _ := procForegroundWin.Call()
	if hwnd == 0 {
		return 0, ""
	}
	var pid uint32
	procWinThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return 0, ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return pid, ""
	}
	defer windows.CloseHandle(h)
	var buf [260]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return pid, ""
	}
	return pid, strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
}

// startFocusWatcher polls the foreground window and latches the focus-hide
// state. setPopoutsFocusHidden is idempotent, so the twice-a-second tick only
// touches windows on an actual change.
//
// The same tick doubles as the overlay-visibility self-heal: the hide latches
// are idempotent, so a Show (or Hide) dropped by the window layer was never
// retried and the overlays stayed wrong until the manual Hide/Show toggle
// re-ran the calls by hand. Reconciling real window visibility against the
// flags every tick makes any such drop correct itself within half a second.
func startFocusWatcher() {
	selfPID := windows.GetCurrentProcessId()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if !GetSettings().HideOverlaysUnfocused {
				setPopoutsFocusHidden(false)
			} else {
				pid, exe := foregroundProc()
				hide := false
				switch {
				case pid == 0 || pid == selfPID:
					// no identifiable window, or our own (overlay drag, main UI)
				case exe == "":
					// unreadable process name — fail open
				case eqFocusExes[exe]:
					// EverQuest is active
				default:
					hide = true
				}
				setPopoutsFocusHidden(hide)
			}
			reconcilePopoutVisibility(true)
			// Keep the Racing Mode overlay glued to the game viewport
			// (internally throttled to every few seconds).
			racingTick()
		}
	}()
}

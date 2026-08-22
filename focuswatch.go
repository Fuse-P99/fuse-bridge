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
	fwUser32             = syscall.NewLazyDLL("user32.dll")
	procForegroundWin    = fwUser32.NewProc("GetForegroundWindow")
	procSetForegroundWin = fwUser32.NewProc("SetForegroundWindow")
	procWinThreadPID     = fwUser32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible  = fwUser32.NewProc("IsWindowVisible")
	procGUIThreadInfo    = fwUser32.NewProc("GetGUIThreadInfo")
	procAsyncKeyState    = fwUser32.NewProc("GetAsyncKeyState")
	procPostMessage      = fwUser32.NewProc("PostMessageW")
)

// foregroundHWND returns the current foreground window handle (0 when none).
func foregroundHWND() uintptr {
	hwnd, _, _ := procForegroundWin.Call()
	return hwnd
}

// restoreForegroundTo hands foreground back to prev if something else has
// taken it since. Windows only grants SetForegroundWindow to the process that
// currently owns the foreground — which is exactly the situation after one of
// our overlay windows activated itself, so the give-back is allowed.
func restoreForegroundTo(prev uintptr) {
	if prev == 0 {
		return
	}
	if cur, _, _ := procForegroundWin.Call(); cur == prev {
		return
	}
	// Never hand foreground to a window that has since been hidden or
	// destroyed (an overlay the same visibility pass hid, a closed app) —
	// that would strand the keyboard on something invisible.
	if vis, _, _ := procIsWindowVisible.Call(prev); vis == 0 {
		return
	}
	procSetForegroundWin.Call(prev)
}

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

// ── stuck move-loop watchdog ─────────────────────────────────────────────────
// A window drag runs DefWindowProc's modal move loop, which holds system-wide
// mouse capture. On a NOACTIVATE overlay whose process isn't foreground that
// loop can miss its button-up and never exit — every click on the machine is
// then swallowed until an alt-tab delivers WM_CANCELMODE. popoutDragBegin
// removes the cause (drags now activate); this watchdog is the backstop for
// any path to the same wedge we haven't met: a move loop on OUR UI thread
// that outlives the mouse button by ~1.5s gets the same WM_CANCELMODE
// alt-tab would send, and the user never notices anything was stuck.
//
// GetGUIThreadInfo reads another thread's input state without attaching to
// it, so this runs safely from the watcher goroutine. The info is OUR UI
// thread's, so hwndMoveSize can only ever be one of our own windows — the
// watchdog cannot cancel anyone else's drag.

type guiThreadInfo struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       [4]int32
}

const (
	guiInMoveSize = 0x0002
	// Native menus (right-click context menus, tray menu) legitimately hold
	// capture with every button UP while the user reads them — the watchdog
	// must never cancel those.
	guiMenuModes = 0x0004 | 0x0008 | 0x0010 // in-menu | system-menu | popup-menu
	wmCancelMode = 0x001F
	vkLButton    = 0x01
	vkRButton    = 0x02
	vkMButton    = 0x04
)

// fwUIThread caches the main UI thread id (every Wails window lives on it).
var fwUIThread uintptr

func uiThreadID() uintptr {
	if fwUIThread != 0 {
		return fwUIThread
	}
	mw := mainWindow
	if mw == nil {
		return 0 // too early — retried next tick
	}
	hwnd := uintptr(mw.NativeWindow())
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	tid, _, _ := procWinThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	fwUIThread = tid
	return tid
}

// moveWedgeTicks counts consecutive ticks spent with our UI thread either
// inside a move loop or holding mouse capture while EVERY mouse button reads
// UP. Three (~1.5s) means an interaction ended without its cleanup — a move
// loop that missed its button-up, or capture orphaned on one of our windows —
// and either state swallows clicks machine-wide. The threshold also spares
// the legitimate keyboard-move case (SC_MOVE + arrows), which our frameless
// overlays don't expose anyway. Known acceptable cost: Chromium's
// middle-click autoscroll mode (capture held, buttons up) gets cancelled
// after ~1.5s — obscure in this UI, and vastly cheaper than the wedge.
var moveWedgeTicks int

func moveLoopWedgeTick() {
	tid := uiThreadID()
	if tid == 0 {
		return
	}
	var gti guiThreadInfo
	gti.cbSize = uint32(unsafe.Sizeof(gti))
	if ok, _, _ := procGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); ok == 0 {
		moveWedgeTicks = 0
		return
	}
	// Open native menus hold capture with the button up by design.
	if gti.flags&guiMenuModes != 0 {
		moveWedgeTicks = 0
		return
	}
	inLoop := gti.flags&guiInMoveSize != 0
	if !inLoop && gti.hwndCapture == 0 {
		moveWedgeTicks = 0
		return
	}
	// Any physical button still held = a live interaction, not a wedge.
	for _, vk := range [...]uintptr{vkLButton, vkRButton, vkMButton} {
		if down, _, _ := procAsyncKeyState.Call(vk); down&0x8000 != 0 {
			moveWedgeTicks = 0
			return
		}
	}
	moveWedgeTicks++
	if moveWedgeTicks < 3 {
		return
	}
	moveWedgeTicks = 0
	hwnd := gti.hwndMoveSize
	if hwnd == 0 {
		hwnd = gti.hwndCapture
	}
	if hwnd != 0 {
		procPostMessage.Call(hwnd, wmCancelMode, 0, 0)
		if inLoop {
			writeLog("focuswatch: cancelled a stuck window move loop (machine-wide input wedge)")
		} else {
			writeLog("focuswatch: released orphaned mouse capture (machine-wide input wedge)")
		}
	}
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
			// Self-heal a stuck window move loop before the user has to
			// discover the alt-tab escape themselves.
			moveLoopWedgeTick()
		}
	}()
}

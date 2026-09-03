package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// noWindowCmd spawns a process with CREATE_NO_WINDOW so no console flashes.
func noWindowCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

// looksLikeEQDir reports whether dir plausibly is an EQ install: the game
// executable is present, or a Logs folder already exists (real or
// VirtualStore-redirected). Requiring only "Logs" breaks fresh installs —
// EQ doesn't create the folder until the first log line is written.
func looksLikeEQDir(dir string) bool {
	if dir == "" {
		return false
	}
	for _, exe := range []string{"eqgame.exe", "everquest.exe"} {
		if _, err := os.Stat(filepath.Join(dir, exe)); err == nil {
			return true
		}
	}
	for _, d := range logsDirCandidates(dir) {
		if _, err := os.Stat(d); err == nil {
			return true
		}
	}
	return false
}

// virtualStoreDir maps an install path to its Windows VirtualStore shadow:
// "C:\Program Files (x86)\Sony\EverQuest" →
// "%LOCALAPPDATA%\VirtualStore\Program Files (x86)\Sony\EverQuest".
// For EQ installs under Program Files run without admin, Windows silently
// redirects the game's file writes (including Logs) there.
func virtualStoreDir(dir string) string {
	la := os.Getenv("LOCALAPPDATA")
	if la == "" || dir == "" {
		return ""
	}
	vol := filepath.VolumeName(dir)
	rel := strings.TrimLeft(strings.TrimPrefix(dir, vol), `\/`)
	if rel == "" {
		return ""
	}
	return filepath.Join(la, "VirtualStore", rel)
}

// logsDirCandidates returns every location EQ logs may land for an install
// dir: the real Logs folder, plus the VirtualStore redirect when applicable.
func logsDirCandidates(eqDir string) []string {
	if eqDir == "" {
		return nil
	}
	out := []string{filepath.Join(eqDir, "Logs")}
	if vs := virtualStoreDir(eqDir); vs != "" {
		out = append(out, filepath.Join(vs, "Logs"))
	}
	return out
}

// eqRootFilePath resolves a file EQ writes to its install root (e.g.
// CHARNAME-Spellbook.txt / -Inventory.txt from /outputfile). Like the Logs
// handling, this checks both the real install dir and the VirtualStore
// redirect — on Program Files installs without admin, /outputfile writes land
// in VirtualStore, so reading only the real dir sees a missing or permanently
// stale snapshot. When both copies exist, the most recently modified wins.
// Returns "" if the file exists in neither location.
func eqRootFilePath(eqDir, filename string) string {
	if eqDir == "" {
		return ""
	}
	best := ""
	var bestMod time.Time
	dirs := []string{eqDir}
	if vs := virtualStoreDir(eqDir); vs != "" {
		dirs = append(dirs, vs)
	}
	for _, d := range dirs {
		p := filepath.Join(d, filename)
		if info, err := os.Stat(p); err == nil && (best == "" || info.ModTime().After(bestMod)) {
			best = p
			bestMod = info.ModTime()
		}
	}
	return best
}

// findEQInstallDir returns the EQ install directory, blocking until it is known.
// It checks the settings cache first (no admin needed), then falls back to
// process detection. If EQ is running as admin and the path cannot be read,
// it prompts the user to set the directory manually in Settings.
func findEQInstallDir() string {
	if cached := GetSettings().EQDirectory; cached != "" {
		if looksLikeEQDir(cached) {
			addStatus("Using cached EQ directory: %s", cached)
			return cached
		}
		addStatus("Cached EQ directory no longer valid, re-detecting...")
	}

	first := true
	blockedLogged := false
	for {
		// Re-check cache each iteration — user may have set it manually.
		if cached := GetSettings().EQDirectory; cached != "" && looksLikeEQDir(cached) {
			return cached
		}

		eqRunning := false
		for _, exe := range []string{"eqgame.exe", "everquest.exe"} {
			if !processExistsInSnapshot(strings.ToLower(exe)) {
				continue
			}
			eqRunning = true
			if dir := pathViaWMI(exe); dir != "" {
				s := GetSettings()
				s.EQDirectory = dir
				UpdateSettings(s)
				return dir
			}
		}

		if first {
			first = false
			if !eqRunning {
				diagProcessScan()
			}
		}

		if eqRunning {
			if !blockedLogged {
				blockedLogged = true
				addStatus("EQ is running but its path cannot be read (likely running as admin).")
				addStatus("Set the EQ install directory manually in Settings → Startup.")
				SetTrayStatus("Set EQ path in Settings → Startup")
			}
		} else {
			blockedLogged = false
			SetTrayStatus("Waiting for EverQuest to start...")
			addStatus("Waiting for EverQuest to start...")
		}

		time.Sleep(5 * time.Second)
	}
}

// installDirFromProcess confirms the process exists via snapshot (fast), then
// queries its path via WMI — which runs as a system service and can read
// elevated (admin-launched) processes that OpenProcess cannot touch.
func installDirFromProcess(exeName string) string {
	if !processExistsInSnapshot(strings.ToLower(exeName)) {
		return ""
	}
	return pathViaWMI(exeName)
}

// processExistsInSnapshot does a cheap toolhelp snapshot scan to confirm the
// named process is running.
func processExistsInSnapshot(lowerExeName string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return false
	}
	for {
		if strings.ToLower(windows.UTF16ToString(pe.ExeFile[:])) == lowerExeName {
			return true
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return false
}

// eqClientRunningCached reports whether an EverQuest client process exists,
// with the answer cached for a few seconds — the trigger engine consults it on
// its 1-second tick while a log-silence "away" episode lasts, and a toolhelp
// scan per second buys nothing. The snapshot lists elevated (admin-launched)
// clients fine: only opening a process needs rights, enumeration doesn't.
//
// A failed snapshot reads as "not running", which errs toward hiding the
// overlays during silence — the behavior every build before the idle-away
// split had unconditionally.
func eqClientRunningCached() bool {
	eqAliveMu.Lock()
	defer eqAliveMu.Unlock()
	if !eqAliveChecked.IsZero() && time.Since(eqAliveChecked) < 10*time.Second {
		return eqAliveVal
	}
	eqAliveVal = processExistsInSnapshot("eqgame.exe") ||
		processExistsInSnapshot("everquest.exe")
	eqAliveChecked = time.Now()
	return eqAliveVal
}

var (
	eqAliveMu      sync.Mutex
	eqAliveVal     bool
	eqAliveChecked time.Time
)

// pathViaWMI queries Win32_Process.ExecutablePath via WMI (PowerShell
// Get-CimInstance). WMI runs as LocalSystem and can access elevated processes
// that a medium-integrity app cannot OpenProcess into.
func pathViaWMI(exeName string) string {
	script := fmt.Sprintf(
		`(Get-CimInstance Win32_Process -Filter "name='%s'" | Select-Object -First 1).ExecutablePath`,
		exeName,
	)
	out, err := noWindowCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		addStatus("WMI query failed for %s: %v", exeName, err)
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Dir(path)
}

// diagProcessScan logs process-scan details on first startup failure so the
// Status window shows diagnostic info.
func diagProcessScan() {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		addStatus("DIAG: CreateToolhelp32Snapshot failed: %v", err)
		return
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		addStatus("DIAG: Process32First failed: %v", err)
		return
	}

	total := 0
	for {
		total++
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if strings.Contains(name, "eq") || strings.Contains(name, "ever") {
			handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID)
			if openErr != nil {
				addStatus("DIAG: '%s' PID=%d OpenProcess err: %v", name, pe.ProcessID, openErr)
			} else {
				var buf [260]uint16
				size := uint32(len(buf))
				if qErr := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); qErr != nil {
					addStatus("DIAG: '%s' PID=%d QueryImageName err: %v", name, pe.ProcessID, qErr)
				} else {
					addStatus("DIAG: '%s' PID=%d path=%q", name, pe.ProcessID, windows.UTF16ToString(buf[:size]))
				}
				windows.CloseHandle(handle)
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	addStatus("DIAG: scanned %d processes total", total)
}

// ── tail-target selection: Blue priority + session locking ──────────────────
//
// The tailer follows ONE log. The old rule — newest mtime wins, re-checked
// every 10 seconds — ping-ponged between two live logs (boxing), and every
// flip runs the full character swap in notifyLogFile: overlays close and
// reopen, timers pause, identify re-fires. Selection is now a LOCK:
//
//  1. An active Blue (home-server) log always wins. While one is live, every
//     other world's log is deliberately ignored — no triggers, timers, or
//     relay for that character until the Blue session ends.
//  2. Otherwise the tail stays locked to the log it is on: Green+Test boxing
//     (and Blue+Blue boxing) stick with the first login, not the last writer.
//  3. The lock releases when the tailed character LEAVES THE GAME — a
//     camp/quit/crash signal (see the left-world signal in tailer.go)
//     followed by its fuse of log silence, or plain inactivity — and the best
//     remaining log takes over, Blue first. A Blue log becoming active steals
//     from an off-Blue lock immediately (that's rule 1).
//
// Single-log players never get a different answer than the old rule gave.

const (
	// tailActiveWindow is how fresh a log's mtime must be to count as a LIVE
	// session — the gate for Blue stealing the tail from an off-Blue lock and
	// for the "ignoring an active log" status note. In-world characters write
	// ambient lines (food/drink, zone chatter) well inside this.
	tailActiveWindow = 2 * time.Minute
	// tailSuccessorWindow is how fresh a log may be to be adopted after the
	// current session ENDS — wider than the active window so a quietly parked
	// second box is still picked up.
	tailSuccessorWindow = 10 * time.Minute
	// tailSlowEndIdle ends the tailed session on silence alone: alt-F4, LD
	// and markerless crashes write no farewell line, so a timeout must exist.
	// Generous, because an AFK-but-in-world character can go quiet for
	// several minutes between ambient lines.
	tailSlowEndIdle = 12 * time.Minute
	// tailSlowEndNoEQ is the faster silence bound once NO EverQuest client
	// process exists at all — with every client closed, nobody is coming back
	// to write this log. Mirrors the trigger engine's idleAwayAfter.
	tailSlowEndNoEQ = 5 * time.Minute
)

// logCandidate is one well-formed eqlog file eligible for tailing.
type logCandidate struct {
	path   string
	mod    time.Time
	isHome bool // Blue (home-server) log
}

// logCandidates lists every eqlog under the Logs dirs, newest first.
func logCandidates(installDir string) []logCandidate {
	var cands []logCandidate
	// Check both the real Logs folder and the VirtualStore redirect — for
	// Program Files installs without admin, the game writes to the latter.
	for _, logsDir := range logsDirCandidates(installDir) {
		entries, err := os.ReadDir(logsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			// Any world's log, not just Blue's — the client follows P99
			// Green/Red and the Fuse test server too (which world it is gates
			// forwarding, see onHomeServer). Require a real eqlog_<Char>_<Token>
			// shape so stray files don't get tailed.
			if !strings.HasPrefix(name, "eqlog_") || !strings.HasSuffix(name, ".txt") {
				continue
			}
			token := serverTokenFromLog(name)
			if token == "" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			cands = append(cands, logCandidate{
				path:   filepath.Join(logsDir, name),
				mod:    info.ModTime(),
				isHome: strings.EqualFold(token, homeServerToken),
			})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].mod.After(cands[j].mod)
	})
	return cands
}

// pickLog returns the newest candidate satisfying pred, skipping exclude.
func pickLog(cands []logCandidate, exclude string, pred func(logCandidate) bool) string {
	for _, c := range cands {
		if c.path == exclude {
			continue
		}
		if pred(c) {
			return c.path
		}
	}
	return ""
}

// bestLog is the full preference order for a fresh attach: newest ACTIVE Blue
// log, else newest log inside the successor window, else newest overall — the
// EQ-closed case, so startup still names the last-played character for the
// deferred popouts.
func bestLog(cands []logCandidate, now time.Time, exclude string) string {
	if p := pickLog(cands, exclude, func(c logCandidate) bool {
		return c.isHome && now.Sub(c.mod) <= tailActiveWindow
	}); p != "" {
		return p
	}
	if p := pickLog(cands, exclude, func(c logCandidate) bool {
		return now.Sub(c.mod) <= tailSuccessorWindow
	}); p != "" {
		return p
	}
	return pickLog(cands, exclude, func(logCandidate) bool { return true })
}

// findActiveLogFile returns the log to attach to when nothing is being tailed
// yet (startup, and the no-log-at-launch recovery).
func findActiveLogFile(installDir string) string {
	return bestLog(logCandidates(installDir), time.Now(), "")
}

// homeLogExistsFor reports whether a HOME-world (Blue) log exists for this
// character name — i.e. the bare name is claimed by a Blue character. Used by
// the per-(character, world) storage-key migration to decide whether bare-name
// buckets belong to a Blue character or to the off-home character now
// attaching.
func homeLogExistsFor(installDir, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	target := "eqlog_" + name + "_" + homeServerToken + ".txt"
	for _, logsDir := range logsDirCandidates(installDir) {
		entries, err := os.ReadDir(logsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(e.Name(), target) {
				return true
			}
		}
	}
	return false
}

// checkForLogFileChange decides, once per staleTick, whether the tailer
// should switch files — the lock rules at the top of this section. Returns ""
// to stay put.
func checkForLogFileChange(installDir, currentPath string) string {
	now := time.Now()
	cands := logCandidates(installDir)
	if currentPath == "" {
		// Never attached (no log existed at launch) — plain best pick.
		return bestLog(cands, now, "")
	}
	curMod := modTime(currentPath)
	if curMod.IsZero() {
		// The tailed file is gone (deleted, or its dir unreadable) — treat it
		// as a fresh attach, stale fallback included.
		return bestLog(cands, now, currentPath)
	}
	curHome := strings.EqualFold(serverTokenFromLog(filepath.Base(currentPath)), homeServerToken)
	if !tailSessionEnded(curMod, now) {
		// Session locked. Exactly one thing outranks a live session: an
		// ACTIVE Blue log while we're tailing another world.
		if !curHome {
			if p := pickLog(cands, currentPath, func(c logCandidate) bool {
				return c.isHome && now.Sub(c.mod) <= tailActiveWindow
			}); p != "" {
				return p
			}
		}
		noteIgnoredLog(cands, currentPath, curHome, now)
		return ""
	}
	// Session over — hand the tail to the best remaining log: newest active
	// Blue, else newest inside the successor window. Nothing recent enough →
	// stay attached: the open handle and offset are untouched, so a log that
	// comes back to life (the character was just AFK) simply continues.
	if p := pickLog(cands, currentPath, func(c logCandidate) bool {
		return c.isHome && now.Sub(c.mod) <= tailActiveWindow
	}); p != "" {
		return p
	}
	return pickLog(cands, currentPath, func(c logCandidate) bool {
		return now.Sub(c.mod) <= tailSuccessorWindow
	})
}

// tailSessionEnded reports whether the tailed log's character has left the
// game: an armed left-world signal (camp countdown in the log, or a dbg.txt
// quit/crash marker — tailer.go) followed by its fuse of silence, or plain
// inactivity long enough that nobody is playing this log. The double idle
// check on the fast path is deliberate: an armed signal whose fuse has
// elapsed but whose log kept writing is a signal ProcessTriggerLine simply
// hasn't disarmed yet, not an ended session.
func tailSessionEnded(curMod, now time.Time) bool {
	if sigAt, fuse, armed := tailLeftSignal(); armed &&
		now.Sub(sigAt) >= fuse && now.Sub(curMod) >= fuse {
		return true
	}
	idle := now.Sub(curMod)
	if idle >= tailSlowEndIdle {
		return true
	}
	return idle >= tailSlowEndNoEQ && !eqClientRunningCached()
}

// tailIgnoreNoteAt throttles the ignored-log status line. Touched only from
// the tailer's staleTick goroutine (the sole checkForLogFileChange caller).
var tailIgnoreNoteAt time.Time

// noteIgnoredLog surfaces the lock in the Status feed when another log is
// being actively written while we deliberately stay put — without it, "my
// other character's triggers do nothing" is undiagnosable from outside.
func noteIgnoredLog(cands []logCandidate, currentPath string, curHome bool, now time.Time) {
	if now.Sub(tailIgnoreNoteAt) < 5*time.Minute {
		return
	}
	for _, c := range cands {
		if c.path == currentPath || now.Sub(c.mod) > tailActiveWindow {
			continue
		}
		reason := "first login keeps the session"
		if curHome {
			reason = "P99 Blue has priority"
		}
		tailIgnoreNoteAt = now
		addStatus("Also seeing activity in %s — staying on %s (%s).",
			filepath.Base(c.path), filepath.Base(currentPath), reason)
		return
	}
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

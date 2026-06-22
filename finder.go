package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// findEQInstallDir returns the EQ install directory. On the first run it blocks
// until a running eqgame.exe or everquest.exe is found and caches the result.
// On subsequent runs it returns immediately from the cached value, avoiding any
// process or WMI queries (and thus the need for admin rights).
func findEQInstallDir() string {
	if cached := GetSettings().EQDirectory; cached != "" {
		if _, err := os.Stat(filepath.Join(cached, "Logs")); err == nil {
			addStatus("Using cached EQ directory: %s", cached)
			return cached
		}
		addStatus("Cached EQ directory no longer valid, re-detecting...")
	}

	first := true
	for {
		for _, exe := range []string{"eqgame.exe", "everquest.exe"} {
			if dir := installDirFromProcess(exe); dir != "" {
				s := GetSettings()
				s.EQDirectory = dir
				UpdateSettings(s)
				return dir
			}
		}
		if first {
			first = false
			diagProcessScan()
		}
		SetTrayStatus("Waiting for EverQuest to start...")
		addStatus("Waiting for EverQuest to start...")
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

// findActiveLogFile returns the most recently modified eqlog_*.txt file under
// the Logs subdirectory.
func findActiveLogFile(installDir string) string {
	logsDir := filepath.Join(installDir, "Logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "eqlog_") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{
			path:    filepath.Join(logsDir, name),
			modTime: info.ModTime(),
		})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	return candidates[0].path
}

// checkForLogFileChange returns a new log path if the current file has been
// stale for more than 10 seconds and a fresher one exists.
func checkForLogFileChange(installDir, currentPath string) string {
	if time.Since(modTime(currentPath)) < 10*time.Second {
		return ""
	}
	newPath := findActiveLogFile(installDir)
	if newPath != "" && newPath != currentPath {
		return newPath
	}
	return ""
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

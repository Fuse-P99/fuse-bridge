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
)

// noWindowCmd creates an exec.Cmd that will not spawn a visible console window.
// Required for GUI apps on Windows — without this, every subprocess opens a
// blank cmd.exe window.
func noWindowCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd
}

// findEQInstallDir locates the EverQuest installation directory by finding the
// running eqgame.exe or everquest.exe process. Blocks until EQ is found.
func findEQInstallDir() string {
	for {
		for _, name := range []string{"eqgame", "everquest"} {
			if dir := installDirFromProcess(name); dir != "" {
				return dir
			}
		}
		SetTrayStatus("Waiting for EverQuest to start...")
		addStatus("Waiting for EverQuest to start...")
		time.Sleep(5 * time.Second)
	}
}

// installDirFromProcess uses PowerShell Get-Process to find the full path of
// a running process by name and returns its directory.
func installDirFromProcess(name string) string {
	script := fmt.Sprintf(
		`try { (Get-Process -Name '%s' -ErrorAction Stop | Select-Object -First 1).MainModule.FileName } catch { '' }`,
		name,
	)
	out, err := noWindowCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Dir(path)
}

// findActiveLogFile scans the Logs subdirectory and returns the path of the
// most recently modified eqlog_*.txt file.
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

// checkForLogFileChange returns a new log file path if a fresher log file has
// been written to more recently than the current one, otherwise "".
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

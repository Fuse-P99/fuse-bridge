package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// findEQInstallDir locates the EverQuest installation directory by finding
// the running eqgame.exe or everquest.exe process and reading its path.
// Blocks until EQ is found, retrying every 5 seconds.
func findEQInstallDir(statusFn func(string)) string {
	for {
		for _, exeName := range []string{"eqgame.exe", "everquest.exe"} {
			dir := installDirFromProcess(exeName)
			if dir != "" {
				return dir
			}
		}
		statusFn("Waiting for EverQuest to start...")
		time.Sleep(5 * time.Second)
	}
}

func installDirFromProcess(exeName string) string {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+exeName, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	pidRe := regexp.MustCompile(`"[^"]+","(\d+)"`)
	for _, line := range lines {
		m := pidRe.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		pid := m[1]
		wmicOut, err := exec.Command("wmic", "process", "where", "ProcessId="+pid, "get", "ExecutablePath", "/format:value").Output()
		if err != nil {
			continue
		}
		for _, wline := range strings.Split(string(wmicOut), "\n") {
			wline = strings.TrimSpace(wline)
			if strings.HasPrefix(wline, "ExecutablePath=") {
				path := strings.TrimPrefix(wline, "ExecutablePath=")
				path = strings.TrimSpace(path)
				if path != "" {
					return filepath.Dir(path)
				}
			}
		}
	}
	return ""
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

// checkForLogFileChange returns a new log file path if a fresher log file
// has been written to more recently than the current one, otherwise "".
func checkForLogFileChange(installDir, currentPath string) string {
	if time.Since(modTime(currentPath)) < 10*time.Second {
		return "" // current file is still active
	}
	newPath := findActiveLogFile(installDir)
	if newPath != "" && newPath != currentPath {
		fmt.Printf("Switching to: %s\n", filepath.Base(newPath))
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

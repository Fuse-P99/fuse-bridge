package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LogArchiveSettings is the Manage-Logs form shape (a projection of the archival
// fields on Settings, with defaults already resolved for display).
type LogArchiveSettings struct {
	Enabled    bool   `json:"enabled"`
	Dir        string `json:"dir"`
	SizeMB     int    `json:"size_mb"`
	DeleteDays int    `json:"delete_days"`
}

// GetLogArchiveSettings returns the current archival config with the default
// directory and size filled in, so the form shows real values even before the
// user has saved anything.
func (a *App) GetLogArchiveSettings() LogArchiveSettings {
	s := GetSettings()
	dir := s.ArchiveLogDir
	if dir == "" {
		dir = defaultArchiveDir(s.EQDirectory)
	}
	size := s.ArchiveSizeMB
	if size == 0 {
		size = 50
	}
	return LogArchiveSettings{
		Enabled:    s.ArchiveLogs,
		Dir:        dir,
		SizeMB:     size,
		DeleteDays: s.ArchiveDeleteDays,
	}
}

// SaveLogArchiveSettings persists the Manage-Logs form. Enabling it runs a pass
// shortly after (still gated on a quiet period) so the user sees it take effect
// without waiting for the next tick.
func (a *App) SaveLogArchiveSettings(in LogArchiveSettings) {
	s := GetSettings()
	s.ArchiveLogs = in.Enabled
	s.ArchiveLogDir = strings.TrimSpace(in.Dir)
	if s.ArchiveSizeMB = in.SizeMB; s.ArchiveSizeMB < 0 {
		s.ArchiveSizeMB = 0
	}
	if s.ArchiveDeleteDays = in.DeleteDays; s.ArchiveDeleteDays < 0 {
		s.ArchiveDeleteDays = 0
	}
	UpdateSettings(s)
	if s.ArchiveLogs {
		go func() {
			time.Sleep(5 * time.Second)
			runLogArchivePass()
		}()
	}
}

// BrowseArchiveDir opens a folder picker for the archive location.
func (a *App) BrowseArchiveDir() string {
	if v3App == nil {
		return ""
	}
	dir, err := v3App.Dialog.OpenFile().
		SetTitle("Select a folder for archived logs").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil || dir == "" {
		return ""
	}
	return dir
}

// defaultArchiveDir resolves the default archive location: an existing
// backup/archive folder in the Logs dir if one is present, else a "Backup"
// folder under the primary Logs dir.
func defaultArchiveDir(eqDir string) string {
	if eqDir == "" {
		return ""
	}
	cands := logsDirCandidates(eqDir)
	for _, logsDir := range cands {
		entries, _ := os.ReadDir(logsDir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			nl := strings.ToLower(e.Name())
			if strings.Contains(nl, "backup") || strings.Contains(nl, "archive") {
				return filepath.Join(logsDir, e.Name())
			}
		}
	}
	if len(cands) > 0 {
		return filepath.Join(cands[0], "Backup")
	}
	return ""
}

// startLogArchiver runs the archival pass periodically. Every pass is gated on a
// fully-quiet period (see runLogArchivePass), so it never touches logs while the
// game is being played.
func startLogArchiver() {
	go func() {
		for range time.Tick(30 * time.Minute) {
			runLogArchivePass()
		}
	}()
}

// runLogArchivePass moves oversized, non-active eqlog files to the archive dir
// and prunes old archives. It is a no-op unless archival is enabled AND the EQ
// logs have been quiet for at least an hour (logIsStale) — the same fully-quiet
// gate the auto-updater uses, so the tailer is never fighting a file move. The
// currently-tailed character's log is skipped regardless (it's the one EQ and
// the tailer hold open).
func runLogArchivePass() {
	s := GetSettings()
	if !s.ArchiveLogs {
		return
	}
	// Low-priority: only run once the game has been idle for an hour.
	if !logIsStale() {
		return
	}
	eqDir := s.EQDirectory
	if eqDir == "" {
		return
	}
	dir := s.ArchiveLogDir
	if dir == "" {
		dir = defaultArchiveDir(eqDir)
	}
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		addStatus("Log archive: cannot create %s: %v", dir, err)
		return
	}
	dirClean := strings.ToLower(filepath.Clean(dir))

	sizeMB := s.ArchiveSizeMB
	if sizeMB == 0 {
		sizeMB = 50
	}
	threshold := int64(sizeMB) * 1024 * 1024

	// Never archive the character currently being tailed.
	skipPrefix := ""
	if currentCharName != "" {
		skipPrefix = "eqlog_" + strings.ToLower(currentCharName) + "_"
	}

	for _, logsDir := range logsDirCandidates(eqDir) {
		// If the archive dir IS this logs dir, skip — a move would just rename in
		// place and the file would qualify again next pass.
		if strings.ToLower(filepath.Clean(logsDir)) == dirClean {
			continue
		}
		entries, _ := os.ReadDir(logsDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			nl := strings.ToLower(e.Name())
			if !strings.HasPrefix(nl, "eqlog_") || !strings.HasSuffix(nl, ".txt") {
				continue
			}
			if skipPrefix != "" && strings.HasPrefix(nl, skipPrefix) {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Size() < threshold {
				continue
			}
			src := filepath.Join(logsDir, e.Name())
			dst := filepath.Join(dir, archivedLogName(e.Name()))
			if err := os.Rename(src, dst); err != nil {
				// Open handle, cross-device move, or permissions — skip and retry
				// next pass rather than risk disrupting anything.
				addStatus("Log archive: could not move %s: %v", e.Name(), err)
				continue
			}
			addStatus("Archived log %s (%d MB) → %s", e.Name(), info.Size()/(1024*1024), dir)
		}
	}

	pruneArchivedLogs(dir, s.ArchiveDeleteDays)
}

// archivedLogName datestamps the archived copy so repeated archives of the same
// character's log (EQ recreates it and it grows again) never collide. The name
// still contains the character, so the Logs tab's "include archived" search
// finds it.
func archivedLogName(name string) string {
	stamp := time.Now().Format("2006-01-02_150405")
	if base, ok := strings.CutSuffix(name, ".txt"); ok {
		return base + "." + stamp + ".txt"
	}
	return name + "." + stamp
}

// pruneArchivedLogs deletes *.txt files in dir older than days (by mod time).
// days <= 0 disables deletion.
func pruneArchivedLogs(dir string, days int) {
	if days <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			addStatus("Deleted archived log %s (older than %d days)", e.Name(), days)
		}
	}
}

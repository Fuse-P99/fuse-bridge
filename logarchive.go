package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LogArchiveSettings is the Manage-Logs form shape (a projection of the archival
// fields on Settings, with defaults already resolved for display). Two surfaces
// edit it — the Archive logs checkbox under Basic Settings on the General tab
// and the Manage Logs dialog on the Logs tab — and both go through the pair of
// methods below, so there is one store and one set of rules behind them.
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

// SaveLogArchiveSettings persists the archival settings from either surface.
//
// Enabling is the moment the defaults are pinned: an unset folder becomes the
// resolved one (an existing Backup/Archive under Logs, else a new Backup) and an
// unset threshold becomes 50 MB, both WRITTEN to the settings rather than left
// for the pass to resolve, so the checkbox and the dialog show the same folder
// and the same number from then on. Enabling also proves the folder can be
// written to — created if need be — and refuses, saving nothing, when it
// cannot: the error is the message the surface shows, and the setting stays
// off rather than on and silently failing every pass. Disabling saves as-is.
//
// A successful enable runs a pass shortly after (still gated on a quiet
// period) so the user sees it take effect without waiting for the next tick.
func (a *App) SaveLogArchiveSettings(in LogArchiveSettings) error {
	s := GetSettings()
	dir := strings.TrimSpace(in.Dir)
	size := in.SizeMB
	if size < 0 {
		size = 0
	}
	days := in.DeleteDays
	if days < 0 {
		days = 0
	}
	if in.Enabled {
		if dir == "" {
			dir = defaultArchiveDir(s.EQDirectory)
		}
		if size == 0 {
			size = 50
		}
		if err := archiveDirUsable(s.EQDirectory, dir); err != nil {
			return err
		}
	}
	s.ArchiveLogs = in.Enabled
	s.ArchiveLogDir = dir
	s.ArchiveSizeMB = size
	s.ArchiveDeleteDays = days
	UpdateSettings(s)
	if s.ArchiveLogs {
		go func() {
			time.Sleep(5 * time.Second)
			runLogArchivePass()
		}()
	}
	return nil
}

// archiveDirUsable is the enable-time check on the archive folder: it must
// exist (it is created here if it doesn't — this is where the default Backup
// folder comes from), it must not be a Logs folder itself (the pass would just
// rename files in place and find them oversized again next time), and a file
// must actually be writable in it. The proof is a real temp file, written and
// removed, because a folder that can be created or listed is not always one
// that can be written to (a Program Files install without elevation, a
// read-only share). Every error is worded for the person reading it.
func archiveDirUsable(eqDir, dir string) error {
	if dir == "" {
		return errors.New("Set your EverQuest directory first — the archive folder lives under its Logs folder.")
	}
	clean := strings.ToLower(filepath.Clean(dir))
	for _, logsDir := range logsDirCandidates(eqDir) {
		if strings.ToLower(filepath.Clean(logsDir)) == clean {
			return errors.New("The archive folder can't be the Logs folder itself — pick a folder inside or beside it.")
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("Can't create the archive folder %s: %s", dir, osErrText(err))
	}
	f, err := os.CreateTemp(dir, ".fusebridge-write-test-*")
	if err != nil {
		return fmt.Errorf("Can't write to the archive folder %s: %s", dir, osErrText(err))
	}
	name := f.Name()
	_, werr := f.WriteString("FuseBridge archive write test\n")
	cerr := f.Close()
	os.Remove(name)
	if werr != nil {
		return fmt.Errorf("Can't write to the archive folder %s: %s", dir, osErrText(werr))
	}
	if cerr != nil {
		return fmt.Errorf("Can't write to the archive folder %s: %s", dir, osErrText(cerr))
	}
	return nil
}

// osErrText is the OS's own reason for a file error ("Access is denied.",
// "The system cannot find the path specified.") without the path the
// *PathError also carries — the messages above already name the folder once.
func osErrText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
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

// defaultArchiveDir resolves the default archive location: a folder already in
// the Logs dir named Backup or Archive (Backup first when both exist), failing
// that one whose name merely contains either word, else a "Backup" folder
// under the primary Logs dir — which does not exist yet; archiveDirUsable
// creates it the moment archiving is switched on. Deterministic on purpose:
// a directory listing's order is nobody's choice, and the folder this names is
// the one written into the settings.
func defaultArchiveDir(eqDir string) string {
	if eqDir == "" {
		return ""
	}
	cands := logsDirCandidates(eqDir)
	for _, logsDir := range cands {
		entries, _ := os.ReadDir(logsDir)
		exact, partial := "", ""
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			nl := strings.ToLower(e.Name())
			switch {
			case nl == "backup" || nl == "archive":
				if exact == "" || nl == "backup" {
					exact = e.Name()
				}
			case strings.Contains(nl, "backup") || strings.Contains(nl, "archive"):
				if partial == "" {
					partial = e.Name()
				}
			}
		}
		if exact != "" {
			return filepath.Join(logsDir, exact)
		}
		if partial != "" {
			return filepath.Join(logsDir, partial)
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

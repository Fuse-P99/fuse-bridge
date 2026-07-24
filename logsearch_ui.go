package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EQ writes each line as "[Mon Jan 02 15:04:05 2006] message", in the player's
// local time. Parsing in time.Local keeps range comparisons aligned with the
// datetime pickers, which are also local (same machine).
const eqLogTimeLayout = "Mon Jan 02 15:04:05 2006"

// Search result hits beyond this cap aren't returned to the UI (the count is
// still accurate); the context radius is how many lines around a hit the
// expanded view shows.
const (
	logSearchMaxHits = 5000
	logContextRadius = 500
)

// LogSearchChar is one entry in the Logs-tab character picker.
type LogSearchChar struct {
	Name    string `json:"name"`
	Class   string `json:"class"`
	Current bool   `json:"current"` // the character whose log is being tailed
}

// GetLogSearchCharacters lists the characters with an eqlog file on this
// machine (the toons actually played here), current character first so the
// picker can default to it.
func (a *App) GetLogSearchCharacters() []LogSearchChar {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n = strings.TrimSpace(n); n == "" {
			return
		}
		if k := strings.ToLower(n); !seen[k] {
			seen[k] = true
			names = append(names, n)
		}
	}

	add(currentCharName)
	for _, n := range logFileCharNames(GetSettings().EQDirectory) {
		add(n)
	}

	sort.Slice(names, func(i, j int) bool {
		ci := strings.EqualFold(names[i], currentCharName)
		cj := strings.EqualFold(names[j], currentCharName)
		if ci != cj {
			return ci
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	out := make([]LogSearchChar, 0, len(names))
	for _, n := range names {
		out = append(out, LogSearchChar{
			Name:    n,
			Class:   trigClassFor(n),
			Current: strings.EqualFold(n, currentCharName),
		})
	}
	return out
}

// LogSearchHit is one matching line. File+LineNo address it for GetLogContext.
type LogSearchHit struct {
	File   string `json:"file"`
	LineNo int    `json:"line_no"` // 1-based within its file
	AtMs   int64  `json:"at_ms"`   // parsed timestamp epoch ms (0 if unparseable)
	Line   string `json:"line"`
}

// LogSearchResult carries the (capped) hit list plus the true total.
type LogSearchResult struct {
	Hits      []LogSearchHit `json:"hits"`
	Total     int            `json:"total"`     // every match, even beyond the cap
	Truncated bool           `json:"truncated"` // Total > len(Hits)
	Files     int            `json:"files"`     // log files scanned
}

// SearchLogs finds lines in `character`'s logs that fall within [startMs, endMs]
// and contain `query` (case-insensitive substring). With includeArchived it also
// searches *.txt files in Logs subdirectories whose filename contains the
// character name.
func (a *App) SearchLogs(character, query string, startMs, endMs int64, includeArchived bool) LogSearchResult {
	res := LogSearchResult{Hits: []LogSearchHit{}}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || strings.TrimSpace(character) == "" {
		return res
	}

	files := logFilesForChar(GetSettings().EQDirectory, character, includeArchived)
	res.Files = len(files)
	start := time.UnixMilli(startMs)
	end := time.UnixMilli(endMs)

	for _, f := range files {
		// A file last modified before the window starts can't hold in-range lines
		// (logs are append-only chronological), so skip it without opening.
		if info, err := os.Stat(f); err == nil && info.ModTime().Before(start) {
			continue
		}
		searchLogFile(f, q, start, end, &res)
	}
	return res
}

// searchLogFile scans one file, appending matches to res. Timestamps carry
// forward so a rare continuation line (no bracketed time) inherits the range of
// the entry it belongs to; because lines are chronological, scanning stops once
// a line's time passes the window end.
func searchLogFile(path, qLower string, start, end time.Time, res *LogSearchResult) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()

	r := bufio.NewReader(fh)
	lineNo := 0
	var lastTS time.Time
	haveTS := false

	for {
		raw, rerr := r.ReadString('\n')
		if len(raw) > 0 {
			lineNo++
			line := strings.TrimRight(raw, "\r\n")
			if ts, ok := parseLogLineTime(line); ok {
				lastTS = ts
				haveTS = true
			}

			inRange := true
			if haveTS {
				if lastTS.Before(start) {
					inRange = false
				} else if lastTS.After(end) {
					return // chronological: nothing later qualifies
				}
			}

			if inRange && strings.Contains(strings.ToLower(line), qLower) {
				res.Total++
				if len(res.Hits) < logSearchMaxHits {
					var at int64
					if haveTS {
						at = lastTS.UnixMilli()
					}
					res.Hits = append(res.Hits, LogSearchHit{
						File: path, LineNo: lineNo, AtMs: at, Line: line,
					})
				} else {
					res.Truncated = true
				}
			}
		}
		if rerr != nil {
			return
		}
	}
}

// LogContext is a window of lines around a hit for the expanded view.
type LogContext struct {
	File   string   `json:"file"`
	Header string   `json:"header"` // file name + modified-time summary
	Lines  []string `json:"lines"`
	Center int      `json:"center"` // index in Lines of the hit line (-1 if not found)
}

// GetLogContext returns the lines logContextRadius before and after lineNo in
// file. The file must live under the EQ Logs tree (guards against arbitrary
// reads from a UI-supplied path).
func (a *App) GetLogContext(file string, lineNo int) LogContext {
	out := LogContext{Lines: []string{}, Center: -1}
	if !logPathAllowed(file) {
		return out
	}
	fh, err := os.Open(file)
	if err != nil {
		return out
	}
	defer fh.Close()

	out.File = file
	out.Header = fileModHeader(file)
	from := lineNo - logContextRadius
	if from < 1 {
		from = 1
	}
	to := lineNo + logContextRadius

	r := bufio.NewReader(fh)
	n := 0
	for {
		raw, rerr := r.ReadString('\n')
		if len(raw) > 0 {
			n++
			if n >= from && n <= to {
				if n == lineNo {
					out.Center = len(out.Lines)
				}
				out.Lines = append(out.Lines, strings.TrimRight(raw, "\r\n"))
			}
			if n > to {
				return out
			}
		}
		if rerr != nil {
			return out
		}
	}
}

// logFilesForChar returns the log files to search for char: the primary
// eqlog_CHAR_*.txt files at the top of each Logs dir, plus — when archived is
// set — any *.txt in a subdirectory whose filename contains the char name.
func logFilesForChar(eqDir, char string, archived bool) []string {
	lower := strings.ToLower(strings.TrimSpace(char))
	if lower == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	addFile := func(p string) {
		ap, err := filepath.Abs(p)
		if err != nil {
			ap = p
		}
		if k := strings.ToLower(ap); !seen[k] {
			seen[k] = true
			out = append(out, ap)
		}
	}

	prefix := "eqlog_" + lower + "_"
	for _, logsDir := range logsDirCandidates(eqDir) {
		entries, _ := os.ReadDir(logsDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := strings.ToLower(e.Name())
			if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".txt") {
				addFile(filepath.Join(logsDir, e.Name()))
			}
		}
		if !archived {
			continue
		}
		// Archived copies live in subdirectories; match any *.txt naming the char.
		filepath.WalkDir(logsDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if filepath.Dir(p) == logsDir {
				return nil // top-level files already handled above
			}
			n := strings.ToLower(d.Name())
			if strings.HasSuffix(n, ".txt") && strings.Contains(n, lower) {
				addFile(p)
			}
			return nil
		})
	}
	return out
}

// parseLogLineTime extracts the bracketed EQ timestamp from a log line.
func parseLogLineTime(line string) (time.Time, bool) {
	if len(line) < 2 || line[0] != '[' {
		return time.Time{}, false
	}
	i := strings.IndexByte(line, ']')
	if i < 0 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(eqLogTimeLayout, line[1:i], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// logPathAllowed reports whether p is inside the EQ install/Logs tree, so a
// UI-supplied path can only reach log files.
func logPathAllowed(p string) bool {
	ap, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	apl := strings.ToLower(filepath.Clean(ap))

	eqDir := GetSettings().EQDirectory
	roots := logsDirCandidates(eqDir)
	if eqDir != "" {
		roots = append(roots, eqDir)
	}
	for _, root := range roots {
		rp, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rpl := strings.ToLower(filepath.Clean(rp))
		if apl == rpl || strings.HasPrefix(apl, rpl+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

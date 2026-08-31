package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
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
	// Window sizing for the expanded file view. Every line becomes a DOM row,
	// so the ceiling is a rendering limit, not a memory one — an uncapped load
	// would hang the client rather than show it anything.
	//
	// EQ log lines run ~75 bytes (median 73 chars + CRLF), so the chunk is
	// ~2.1MB and the ceiling ~6.4MB. Both are far below a log due for
	// archiving: at 50MB a file holds ~700,000 lines. That gap is the whole
	// reason the window walks in chunks instead of pretending to load it all.
	logWindowChunk = 30000
	logWindowMax   = 90000
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
	// First is the absolute file line number of Lines[0], 1-based. Without it
	// an index into Lines means nothing outside the load that produced it —
	// and holding the reader's scroll position across a widening load is
	// exactly the problem of translating an index from one window to another.
	First int `json:"first"`
	// Total is the file's full line count, so the UI can put a number on the
	// "load entire file" offer and say what fraction it ended up showing.
	Total int `json:"total"`
	// Full reports that Lines covers the whole file — the signal that there is
	// no more to load in either direction.
	Full bool `json:"full"`
}

// GetLogContext returns the lines logContextRadius before and after lineNo in
// file. The file must live under the EQ Logs tree (guards against arbitrary
// reads from a UI-supplied path).
func (a *App) GetLogContext(file string, lineNo int) LogContext {
	return readLogWindow(file, lineNo, lineNo-logContextRadius, lineNo+logContextRadius)
}

// GetLogFile loads the whole file, for the case where it fits under
// logWindowMax. When it doesn't it degrades to a chunk centred on `around`
// rather than refusing: `around` is the line the reader is looking at, and
// they must not be thrown somewhere else.
func (a *App) GetLogFile(file string, around int) LogContext {
	total := countLogLines(file)
	if total <= logWindowMax {
		return readLogWindow(file, around, 1, total)
	}
	half := logWindowChunk / 2
	from := around - half
	if from < 1 {
		from = 1
	}
	to := from + logWindowChunk - 1
	if to > total {
		to, from = total, total-logWindowChunk+1
	}
	return readLogWindow(file, around, from, to)
}

// GetLogRange serves an explicit line range, for walking a file too large to
// hold at once. The caller owns the policy — which direction it grew, and how
// far — because it knows where the reader is; this only enforces the ceiling.
//
// A span past logWindowMax is trimmed from the END FURTHEST from `around`, so
// extending upward drops lines off the bottom and vice versa. The reader's own
// position always survives the trim.
func (a *App) GetLogRange(file string, from, to, around int) LogContext {
	if from < 1 {
		from = 1
	}
	if to < from {
		to = from
	}
	if to-from+1 > logWindowMax {
		if around-from < to-around {
			to = from + logWindowMax - 1 // reader is near the top; trim the bottom
		} else {
			from = to - logWindowMax + 1
		}
	}
	return readLogWindow(file, around, from, to)
}

// OpenLogFileWith hands the log to Windows' own "How do you want to open this
// file?" chooser. No log this app can render is bigger than what a real editor
// handles, so the honest answer for a 700,000-line file is to let the user open
// it in a tool built for that — and the shell chooser is that prompt, already
// listing what they actually have installed.
//
// Deliberately not ShellExecute "open": that silently launches whatever the
// .txt association happens to be, which answers a question the user was asked
// but never got to answer.
func (a *App) OpenLogFileWith(file string) string {
	if !logPathAllowed(file) {
		return "That file is outside the EQ Logs folder."
	}
	if _, err := os.Stat(file); err != nil {
		return "That log file is no longer there."
	}
	// CREATE_NO_WINDOW only — HideWindow would apply SW_HIDE to the first
	// top-level window the process shows, which is the chooser itself.
	cmd := exec.Command("rundll32.exe", "shell32.dll,OpenAs_RunDLL", file)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		return "Could not open the file chooser: " + err.Error()
	}
	// Reaped in the background so the chooser (which lives as long as the user
	// leaves it open) doesn't leave a zombie behind.
	go cmd.Wait()
	return ""
}

// countLogLines counts lines without retaining any of them, so the size of the
// file bounds the time this takes and not the memory.
func countLogLines(file string) int {
	if !logPathAllowed(file) {
		return 0
	}
	fh, err := os.Open(file)
	if err != nil {
		return 0
	}
	defer fh.Close()
	return countRest(fh)
}

// countRest counts the lines remaining in r, assuming it is positioned at the
// start of one. Used both to size a whole file and to finish counting after a
// window has been filled — past that point the lines are needed as a number,
// not as strings, and building them would dominate the cost on a large log.
func countRest(r io.Reader) int {
	buf := make([]byte, 256*1024)
	n, trailing := 0, false
	for {
		read, rerr := r.Read(buf)
		if read > 0 {
			n += bytes.Count(buf[:read], []byte{'\n'})
			trailing = buf[read-1] != '\n'
		}
		if rerr != nil {
			break
		}
	}
	// A final line with no newline after it is still a line.
	if trailing {
		n++
	}
	return n
}

// readLogWindow reads file lines [from, to] (1-based, inclusive, clamped),
// marking `center` if it lands inside. The file must live under the EQ Logs
// tree — this guards against arbitrary reads from a UI-supplied path.
func readLogWindow(file string, center, from, to int) LogContext {
	out := LogContext{Lines: []string{}, Center: -1}
	if !logPathAllowed(file) {
		return out
	}
	fh, err := os.Open(file)
	if err != nil {
		return out
	}
	defer fh.Close()

	if from < 1 {
		from = 1
	}
	out.File = file
	out.Header = fileModHeader(file)
	out.First = from

	r := bufio.NewReader(fh)
	n := 0
	for {
		raw, rerr := r.ReadString('\n')
		if len(raw) > 0 {
			n++
			if n >= from && n <= to {
				if n == center {
					out.Center = len(out.Lines)
				}
				out.Lines = append(out.Lines, strings.TrimRight(raw, "\r\n"))
			}
			if n > to {
				// Window filled. The rest of the file still has to be counted
				// — Total is what tells the UI whether anything remains to
				// load — but not materialised.
				n += countRest(r)
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	out.Total = n
	out.Full = from <= 1 && len(out.Lines) >= n
	if len(out.Lines) == 0 {
		out.First = 0
	}
	return out
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

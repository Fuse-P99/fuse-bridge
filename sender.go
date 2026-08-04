package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// errAuthRejected marks a 401 from the server: the token this client holds is
// no longer valid (revoked/unlinked server-side). Not retryable.
var errAuthRejected = errors.New("authorization rejected")

const (
	maxQueueSize   = 500
	batchInterval  = 2 * time.Second
	retryBaseDelay = 5 * time.Second
	retryMaxDelay  = 5 * time.Minute
	// queueStaleAge drops lines that grew old waiting in the queue rather than
	// delivering them late.
	//
	// The tailer checks a line's age when it READS it, and resyncs to the end of
	// the log if it finds old content — that covers re-reading an archived or
	// rotated file. It does not cover this: a line that was perfectly fresh when
	// read, then sat here while sends failed. Retries back off to five minutes
	// and never give up, so an outage — a server restart, a dead link, a laptop
	// suspended mid-raid — parks a batch here and flushes it whenever the
	// connection returns.
	//
	// What arrives then is an hours-old slice of guild chat, and the server's
	// own replay guard only drops the part older than two hours; anything
	// younger passes, and its dedup entries expired long ago, so it posts to
	// Discord a second time. That is the message storm. Matching the server's
	// window here means the client never ships what the server would reject,
	// and stops just short of the age where a late line becomes a lie.
	queueStaleAge = 2 * time.Hour
)

type Sender struct {
	serverURL    string
	mu           sync.Mutex
	queue        []string
	client       *http.Client
	wasConnected bool
	auth401s     int           // consecutive 401 responses (token revoked / not linked)
	FlushNow     chan struct{} // signal to flush immediately without waiting for the ticker
	OnConnect    func()        // called when a send succeeds after being disconnected
	OnDisconnect func()        // called when a send fails after being connected
}

func NewSender(serverURL string) *Sender {
	return &Sender{
		serverURL: serverURL,
		client:    &http.Client{Timeout: 10 * time.Second},
		FlushNow:  make(chan struct{}, 8),
	}
}

// freshLines splits a batch into the lines still worth sending and a count of
// those that aged out. Lines with no parseable EQ timestamp are always kept —
// they can't be judged, and most of them (MOTD, /who output) matter.
func freshLines(batch []string) ([]string, int) {
	now := time.Now()
	out := batch[:0:0] // never alias the caller's array
	dropped := 0
	for _, line := range batch {
		if lt := logLineTime(line); !lt.IsZero() {
			d := now.Sub(lt)
			if d < 0 {
				d = -d
			}
			if d > queueStaleAge {
				dropped++
				continue
			}
		}
		out = append(out, line)
	}
	return out, dropped
}

// dropSent removes n delivered lines from the front of the queue.
//
// The bound check is not paranoia: Enqueue trims the OLDEST entry when the
// queue is full, so a burst arriving during a slow send can shrink the queue
// under a batch that was copied from it. Slicing past the end would panic on
// exactly the busiest raid moment this is meant to survive.
func (s *Sender) dropSent(n int) {
	s.mu.Lock()
	if n >= len(s.queue) {
		s.queue = nil
	} else {
		s.queue = s.queue[n:]
	}
	s.mu.Unlock()
}

func (s *Sender) Enqueue(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) >= maxQueueSize {
		s.queue = s.queue[1:] // drop oldest to make room
	}
	s.queue = append(s.queue, line)
}

// Run reads from the lines channel, batches them, and sends to the server.
// Retries with exponential backoff on failure.
//
// Unlinked mode: while no per-client token exists, nothing is sent — the queue
// is discarded silently instead of hammering the server with requests that can
// only 401. The gate is re-checked every tick, so completing the Discord link
// flow brings the sender to life without a restart.
func (s *Sender) Run(lines <-chan string, done <-chan struct{}) {
	// Startup ping: verify connectivity and light up the tray icon immediately,
	// independent of whether any log lines have been seen yet.
	if !IsLinked() {
		addStatus("Not linked — log forwarding paused. Link your Discord account on the General tab to enable online features.")
	} else if err := s.send([]string{}); err == nil {
		s.wasConnected = true
		if s.OnConnect != nil {
			s.OnConnect()
		}
	} else {
		addStatus("Server ping failed: %v", err)
		if s.OnDisconnect != nil {
			s.OnDisconnect()
		}
	}

	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	backoff := retryBaseDelay

	trySend := func() {
		if !IsLinked() {
			// Drop whatever accumulated; these lines can never be delivered.
			s.mu.Lock()
			s.queue = nil
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		batch := make([]string, len(s.queue))
		copy(batch, s.queue)
		s.mu.Unlock()

		// Age the batch before it goes anywhere. Stale lines are still counted
		// as handled — they leave the queue either way; the point is that they
		// don't reach Discord.
		fresh, dropped := freshLines(batch)
		if dropped > 0 {
			addStatus("Dropped %d log line(s) that aged past %s waiting to send (connection was down).",
				dropped, queueStaleAge)
		}
		if len(fresh) == 0 {
			s.dropSent(len(batch))
			return
		}

		if err := s.send(fresh); err != nil {
			if errors.Is(err, errAuthRejected) {
				// Token present but refused — revoked server-side. One clear
				// message (after a few tries, in case of a server hiccup)
				// instead of endless retry spam; drop the batch.
				s.auth401s++
				if s.auth401s == 3 {
					addStatus("Server rejected this client's credentials — relink your Discord account on the General tab.")
					if s.wasConnected {
						s.wasConnected = false
						if s.OnDisconnect != nil {
							s.OnDisconnect()
						}
					}
				}
				s.dropSent(len(batch))
				return
			}
			addStatus("Send failed (%v), retrying in %s", err, backoff)
			if s.wasConnected {
				s.wasConnected = false
				if s.OnDisconnect != nil {
					s.OnDisconnect()
				}
			}
			time.Sleep(backoff)
			backoff *= 2
			if backoff > retryMaxDelay {
				backoff = retryMaxDelay
			}
		} else {
			s.dropSent(len(batch))
			backoff = retryBaseDelay
			s.auth401s = 0
			if !s.wasConnected {
				s.wasConnected = true
				if s.OnConnect != nil {
					s.OnConnect()
				}
			}
		}
	}

	for {
		select {
		case <-done:
			return
		case line := <-lines:
			s.Enqueue(line)
		case <-s.FlushNow:
			trySend()
		case <-ticker.C:
			trySend()
		}
	}
}

type submitPayload struct {
	Lines   []string `json:"lines"`
	Toon    string   `json:"toon"`
	Version string   `json:"version"`
	// ProcForward tells the server this client forwards its own proc/resist
	// effect lines (Proc or Resist messages enabled), so the server counts this
	// tank's procs from those and ignores its redundant guild "PROC -" macro.
	ProcForward bool `json:"proc_forward"`
	// TzOffsetMin is this machine's UTC offset in minutes (east positive). EQ
	// stamps log lines in local time; reporting the offset lets the server
	// resolve them to absolute instants and run its replay guard at ±2h instead
	// of the 24h it needs when the timezone is unknown. Taken from the system
	// clock at send time so DST is already applied.
	TzOffsetMin int `json:"tz_offset_min"`
}

func (s *Sender) send(lines []string) error {
	st := GetSettings()
	_, tzSecs := time.Now().Zone()
	body, _ := json.Marshal(submitPayload{
		Lines:       lines,
		Toon:        currentCharName,
		Version:     clientVersion,
		ProcForward: st.ProcMessages || st.ResistMessages,
		TzOffsetMin: tzSecs / 60,
	})
	req, err := http.NewRequest(http.MethodPost, s.serverURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errAuthRejected
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

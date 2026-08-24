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

// Delivery is best-effort and deliberately has no retry. A batch gets one
// attempt; if it fails, it's gone.
//
// Nothing forwarded here is worth delivering late. Guild chat mirrored minutes
// after the fact reads as confusion in #guild-stream; CH and engage calls drive
// live overlays and are noise once the moment has passed. Slain lines are worse
// than useless late — they set a mob's time of death, so a delayed one records
// the wrong ToD rather than a missing one. The only payload that genuinely
// survived a delay was /who for attendance, and that isn't worth what retrying
// costs: retry is what turned a routine outage into hours-old traffic arriving
// in one flush, past its dedup window, reposting a raid's worth of chat.
//
// Server restarts are timed away from engaged mobs and loot bidding, so the
// realistic loss from dropping a failed batch is a few seconds of chat that
// other clients and the Fuselog stream relay anyway.
const (
	maxQueueSize  = 500
	batchInterval = 2 * time.Second
	// queueStaleAge is the backstop for the one way a line can still go stale
	// without retries: the machine suspending with a batch queued, then waking
	// hours later and sending it. Matched to the server's replay window, so the
	// client never ships what the server would reject.
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
// The bound check guards the one way the count can outrun the queue: Enqueue
// trims the OLDEST entry when full, so anything that shrinks the queue between
// the copy and this call leaves n too large, and slicing past the end panics —
// on exactly the busiest raid moment this is meant to survive. The window is
// tiny now that the drop follows the copy immediately, which is the point of
// keeping the check rather than reasoning about who runs when.
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

// Run reads from the lines channel, batches them on a short tick, and makes one
// send attempt per batch. Failures are logged and dropped — see the note on
// queueStaleAge above for why nothing here is retried.
//
// The queue is a batching buffer, not a delivery guarantee: it exists so a busy
// raid costs one request every couple of seconds instead of one per line.
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

		// The batch leaves the queue the moment we attempt it, before we know
		// whether it worked. That IS the no-retry policy, expressed once: no
		// later branch can put it back, and lines arriving during the send stay
		// queued for the next tick instead of being re-sent with this one.
		s.dropSent(len(batch))

		// Only the machine suspending mid-batch can make these old now, but the
		// check is cheap and the server would reject them anyway.
		fresh, dropped := freshLines(batch)
		if dropped > 0 {
			addStatus("Dropped %d log line(s) that aged past %s before sending (machine asleep?).",
				dropped, queueStaleAge)
		}
		if len(fresh) == 0 {
			return
		}

		if err := s.send(fresh); err != nil {
			if errors.Is(err, errAuthRejected) {
				// Token present but refused — revoked server-side. One clear
				// message (after a few tries, in case of a server hiccup)
				// instead of one per batch.
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
				return
			}
			// Said once per disconnection, not once per failed batch: a server
			// restart would otherwise print this every two seconds.
			if s.wasConnected {
				addStatus("Send failed (%v) — %d line(s) dropped. Not retried; live data is only useful live.",
					err, len(fresh))
				s.wasConnected = false
				if s.OnDisconnect != nil {
					s.OnDisconnect()
				}
			}
			return
		}
		s.auth401s = 0
		if !s.wasConnected {
			s.wasConnected = true
			if s.OnConnect != nil {
				s.OnConnect()
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

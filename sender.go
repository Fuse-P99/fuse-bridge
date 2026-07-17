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

		if err := s.send(batch); err != nil {
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
				s.mu.Lock()
				s.queue = s.queue[len(batch):]
				s.mu.Unlock()
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
			s.mu.Lock()
			s.queue = s.queue[len(batch):]
			s.mu.Unlock()
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
}

func (s *Sender) send(lines []string) error {
	body, _ := json.Marshal(submitPayload{Lines: lines, Toon: currentCharName, Version: clientVersion})
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

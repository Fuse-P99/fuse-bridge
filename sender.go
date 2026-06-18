package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	maxQueueSize   = 500
	batchInterval  = 2 * time.Second
	retryBaseDelay = 5 * time.Second
	retryMaxDelay  = 5 * time.Minute
)

type Sender struct {
	serverURL string
	apiKey    string
	mu        sync.Mutex
	queue     []string
	client    *http.Client
}

func NewSender(serverURL, apiKey string) *Sender {
	return &Sender{
		serverURL: serverURL,
		apiKey:    apiKey,
		client:    &http.Client{Timeout: 10 * time.Second},
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
func (s *Sender) Run(lines <-chan string, done <-chan struct{}) {
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	backoff := retryBaseDelay

	for {
		select {
		case <-done:
			return
		case line := <-lines:
			s.Enqueue(line)
		case <-ticker.C:
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.mu.Unlock()
				continue
			}
			batch := make([]string, len(s.queue))
			copy(batch, s.queue)
			s.mu.Unlock()

			if err := s.send(batch); err != nil {
				fmt.Printf("Send failed (%v), retrying in %s\n", err, backoff)
				time.Sleep(backoff)
				backoff *= 2
				if backoff > retryMaxDelay {
					backoff = retryMaxDelay
				}
			} else {
				// Success: clear the sent lines from queue
				s.mu.Lock()
				s.queue = s.queue[len(batch):]
				s.mu.Unlock()
				backoff = retryBaseDelay
			}
		}
	}
}

type submitPayload struct {
	Lines []string `json:"lines"`
}

func (s *Sender) send(lines []string) error {
	body, _ := json.Marshal(submitPayload{Lines: lines})
	req, err := http.NewRequest(http.MethodPost, s.serverURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

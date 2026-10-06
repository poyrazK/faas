// Package eventdelivery provides the same Events & Delivery acceptance gate
// for process E2E tests and explicitly provisioned staging fixtures.
package eventdelivery

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/events"
)

// Preparation registers one isolated test event before publication.
type Preparation struct {
	Source         string `json:"source"`
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	SubscriptionID string `json:"subscription_id"`
	Fail           bool   `json:"fail"`
}

// ConsumerStats measures requests at the application boundary, including
// requests that fail. Effects is an idempotent, in-memory fixture effect.
type ConsumerStats struct {
	Attempts            int      `json:"attempts"`
	Failures            int      `json:"failures"`
	SuccessfulResponses int      `json:"successful_responses"`
	Effects             int      `json:"effects"`
	InvocationIDs       []string `json:"invocation_ids"`
}

type consumerEvent struct {
	Preparation
	ConsumerStats
}

// Consumer is a dedicated acceptance application, not a production inbox.
// A fresh fixture process is required for each staging gate. Its control
// endpoints require a token and never operate on unregistered events.
type Consumer struct {
	mu     sync.Mutex
	token  string
	events map[[2]string]*consumerEvent
}

func NewConsumer(token string) (*Consumer, error) {
	if token == "" {
		return nil, fmt.Errorf("acceptance consumer requires a control token")
	}
	return &Consumer{token: token, events: make(map[[2]string]*consumerEvent)}, nil
}

func (c *Consumer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	if r.URL.Path == "/__gate/events" || r.URL.Path == "/__gate/recover" {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gate-Control-Token")), []byte(c.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c.control(w, r)
		return
	}
	if r.URL.Path != "/" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	c.deliver(w, r)
}

func (c *Consumer) control(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/__gate/events" {
		c.stats(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var p Preparation
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p) != nil || p.Source == "" || p.EventID == "" {
		http.Error(w, "invalid preparation", http.StatusBadRequest)
		return
	}
	key := [2]string{p.Source, p.EventID}
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.URL.Path == "/__gate/recover" {
		if event := c.events[key]; event != nil {
			event.Fail = false
		} else {
			http.NotFound(w, r)
			return
		}
	} else {
		if p.EventType == "" || p.SubscriptionID == "" {
			http.Error(w, "type and subscription required", http.StatusBadRequest)
			return
		}
		// Bound this test-only ledger and refuse identity reuse, which could
		// otherwise erase duplicate-delivery evidence from an earlier run.
		if c.events[key] != nil || len(c.events) >= 128 {
			http.Error(w, "fixture identity exists or ledger full", http.StatusConflict)
			return
		}
		c.events[key] = &consumerEvent{Preparation: p, ConsumerStats: ConsumerStats{InvocationIDs: []string{}}}
	}
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (c *Consumer) stats(w http.ResponseWriter, r *http.Request) {
	key := [2]string{r.URL.Query().Get("source"), r.URL.Query().Get("event_id")}
	c.mu.Lock()
	defer c.mu.Unlock()
	if event := c.events[key]; event != nil {
		_ = json.NewEncoder(w).Encode(event.ConsumerStats)
	} else {
		http.NotFound(w, r)
	}
}

func (c *Consumer) deliver(w http.ResponseWriter, r *http.Request) {
	var envelope events.Envelope
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&envelope) != nil {
		http.Error(w, "invalid envelope", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	event := c.events[[2]string{envelope.Source, envelope.ID}]
	if event == nil || envelope.Validate() != nil || envelope.Type != event.EventType ||
		r.Header.Get("X-Gregale-Event-Id") != envelope.ID ||
		r.Header.Get("X-Gregale-Event-Source") != envelope.Source ||
		r.Header.Get("X-Gregale-Event-Type") != envelope.Type ||
		r.Header.Get("X-Gregale-Event-Subscription-Id") != event.SubscriptionID ||
		r.Header.Get("X-Faas-Invocation-Id") == "" {
		http.Error(w, "unregistered event or incorrect delivery headers", http.StatusUnprocessableEntity)
		return
	}
	if event.Attempts >= 64 {
		http.Error(w, "fixture attempt ledger full", http.StatusConflict)
		return
	}
	event.Attempts++
	event.InvocationIDs = append(event.InvocationIDs, r.Header.Get("X-Faas-Invocation-Id"))
	if event.Fail {
		event.Failures++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"acceptance consumer unavailable"}`))
		return
	}
	event.SuccessfulResponses++
	if event.Effects == 0 {
		event.Effects = 1
	}
	_, _ = w.Write([]byte(`{"ok":true}`))
}

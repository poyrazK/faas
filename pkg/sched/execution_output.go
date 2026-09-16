package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/state"
)

// executionOutputEventSink persists bounded guest output as soon as it
// arrives. The event store remains optional so older scheduler fixtures and
// mixed-version deployments keep the unary execution path.
type executionOutputEventSink struct {
	store       state.ExecutionEventStore
	accountID   string
	executionID string
	now         func() time.Time

	mu        sync.Mutex
	persisted bool
}

func newExecutionOutputEventSink(store state.ExecutionEventStore, accountID, executionID string, now func() time.Time) *executionOutputEventSink {
	if now == nil {
		now = time.Now
	}
	return &executionOutputEventSink{store: store, accountID: accountID, executionID: executionID, now: now}
}

func (s *executionOutputEventSink) Receive(ctx context.Context, stream string, chunk []byte) error {
	if s == nil || s.store == nil {
		return errors.New("sched: live execution output sink is unavailable")
	}
	var eventType state.ExecutionEventType
	switch stream {
	case "stdout":
		eventType = state.ExecutionEventStdout
	case "stderr":
		eventType = state.ExecutionEventStderr
	default:
		return errors.New("sched: live execution output stream is invalid")
	}
	for start := 0; start < len(chunk); {
		end := start + state.ExecutionEventMaxBytes/8
		if end > len(chunk) {
			end = len(chunk)
		}
		payload, err := json.Marshal(struct {
			Chunk string `json:"chunk"`
		}{Chunk: string(chunk[start:end])})
		if err != nil {
			return errors.New("sched: live execution output could not be encoded")
		}
		if _, err := s.store.AppendExecutionEvent(ctx, s.accountID, s.executionID, eventType, payload, s.now().UTC()); err != nil {
			return errors.New("sched: live execution output could not be persisted")
		}
		start = end
		s.mu.Lock()
		s.persisted = true
		s.mu.Unlock()
	}
	return nil
}

func (s *executionOutputEventSink) Persisted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persisted
}

var _ executionproto.OutputReceiver = (*executionOutputEventSink)(nil).Receive

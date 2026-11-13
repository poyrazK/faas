package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/internal/utf8chunk"
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
	pending   map[string][]byte
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[string][]byte)
	}
	data := append(s.pending[stream], chunk...)
	s.pending[stream] = nil
	return s.persist(ctx, stream, data, false)
}

func (s *executionOutputEventSink) persist(ctx context.Context, stream string, data []byte, final bool) error {
	var eventType state.ExecutionEventType
	switch stream {
	case "stdout":
		eventType = state.ExecutionEventStdout
	case "stderr":
		eventType = state.ExecutionEventStderr
	default:
		return errors.New("sched: live execution output stream is invalid")
	}
	for len(data) > 0 {
		end := utf8chunk.Prefix(data, state.ExecutionEventMaxBytes/8, final)
		if end == 0 {
			s.pending[stream] = append([]byte(nil), data...)
			return nil
		}
		payload, err := json.Marshal(struct {
			Chunk string `json:"chunk"`
		}{Chunk: string(data[:end])})
		if err != nil {
			return errors.New("sched: live execution output could not be encoded")
		}
		if _, err := s.store.AppendExecutionEvent(ctx, s.accountID, s.executionID, eventType, payload, s.now().UTC()); err != nil {
			return errors.New("sched: live execution output could not be persisted")
		}
		data = data[end:]
		s.persisted = true
	}
	return nil
}

// Flush preserves a final incomplete (invalid) rune using the same JSON text
// conversion as the receipt. Each stream has independent decoder state.
func (s *executionOutputEventSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stream := range []string{"stdout", "stderr"} {
		if err := s.persist(ctx, stream, s.pending[stream], true); err != nil {
			return err
		}
		delete(s.pending, stream)
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

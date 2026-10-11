package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockingAuditStore struct {
	mu      sync.Mutex
	kinds   []string
	release chan struct{}
}

func (s *blockingAuditStore) AppendEvent(ctx context.Context, _ string, kind string, _ *string, _ []byte) error {
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.kinds = append(s.kinds, kind)
	s.mu.Unlock()
	return nil
}

func (s *blockingAuditStore) written() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.kinds...)
}

// Request-path audit must not wait on Postgres: rows are written by the
// background writer, and a saturated queue drops events instead of blocking
// the request that emitted them.
func TestAsyncAuditStoreWritesInBackgroundAndDropsWhenFull(t *testing.T) {
	store := &blockingAuditStore{release: make(chan struct{})}
	async := newAsyncAuditStore(t.Context(), store, 2, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 { // writer holds one, queue holds two, the rest drop
			_ = async.AppendEvent(context.Background(), "gatewayd", "edge_rule.ip_denied", nil, []byte(`{}`))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AppendEvent blocked on a stalled audit store")
	}
	if async.dropped.Load() == 0 {
		t.Fatal("full queue did not drop events")
	}

	close(store.release)
	deadline := time.Now().Add(2 * time.Second)
	for len(store.written()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(store.written()) == 0 {
		t.Fatal("queued audit events were never written")
	}
}

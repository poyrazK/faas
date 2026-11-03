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

// O22: a flood of CORS-match rows fills only the informational queue; the
// authn failures and denials emitted during it are all written, ahead of the
// matches still waiting.
func TestAsyncAuditStoreKeepsEvidenceThroughAMatchFlood(t *testing.T) {
	store := &blockingAuditStore{release: make(chan struct{})}
	async := newAsyncAuditStoreWithInfo(t.Context(), store, 8, 4, nil)

	for range 100 {
		_ = async.AppendEvent(context.Background(), "gatewayd", "edge_rule.cors_matched", nil, []byte(`{}`))
	}
	evidence := []string{"instances.authn_invalid", "edge_rule.ip_blocked", "instances.authn_missing"}
	for _, kind := range evidence {
		_ = async.AppendEvent(context.Background(), "gatewayd", kind, nil, []byte(`{}`))
	}
	if n := async.dropped.Load(); n != 0 {
		t.Fatalf("evidence rows dropped during a match flood: %d", n)
	}
	if async.droppedInfo.Load() == 0 {
		t.Fatal("the match flood was not bounded by its own queue")
	}

	close(store.release)
	deadline := time.Now().Add(2 * time.Second)
	for len(store.written()) < 1+len(evidence) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := store.written()
	if len(got) < 1+len(evidence) {
		t.Fatalf("written = %v", got)
	}
	// The writer may already hold one match row when the evidence arrives;
	// after it, every evidence row precedes the queued matches.
	if got[0] != "edge_rule.cors_matched" {
		got = append([]string{""}, got...)
	}
	for i, kind := range evidence {
		if got[1+i] != kind {
			t.Fatalf("written order = %v; evidence must be drained before queued matches", got)
		}
	}
}

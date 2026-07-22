package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// fakeResolver maps node id -> instance id for the sink under test.
// Issue #98 / ADR-028: the dial key is now the compute_node.id the
// handler forwarded to via the vmmd ForwardHTTP RPC, not the host:port
// addr.
type fakeResolver map[string]string

func (f fakeResolver) InstanceIDForNodeID(nodeID string) (string, bool) {
	id, ok := f[nodeID]
	return id, ok
}

// fakeReporter captures the last batch handed to ReportActivity.
type fakeReporter struct {
	mu    sync.Mutex
	calls int
	last  []state.InstanceTouch
	err   error
}

func (r *fakeReporter) ReportActivity(_ context.Context, touches []state.InstanceTouch) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return 0, r.err
	}
	r.last = touches
	return len(touches), nil
}

func TestSchedFlushSink_ResolvesAndReports(t *testing.T) {
	resolve := fakeResolver{"10.0.0.2:8080": "i-1", "10.0.0.3:8080": "i-2"}
	rep := &fakeReporter{}
	s := newSchedFlushSink(resolve, rep, testLogger())

	t0 := time.UnixMilli(1_700_000_000_000)
	s.Touch("10.0.0.2:8080", t0)
	s.Touch("10.0.0.2:8080", t0.Add(2*time.Second)) // newer wins
	s.Touch("10.0.0.3:8080", t0)
	s.Touch("10.0.0.9:8080", t0) // unresolved → dropped

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if rep.calls != 1 {
		t.Fatalf("ReportActivity calls = %d, want 1", rep.calls)
	}
	byID := map[string]time.Time{}
	for _, tc := range rep.last {
		byID[tc.InstanceID] = tc.LastRequest
	}
	if len(byID) != 2 {
		t.Fatalf("touches = %+v, want 2 (i-1,i-2; i-3 unresolved dropped)", rep.last)
	}
	if !byID["i-1"].Equal(t0.Add(2 * time.Second)) {
		t.Errorf("i-1 time = %v, want newest %v", byID["i-1"], t0.Add(2*time.Second))
	}
	if _, ok := byID["i-2"]; !ok {
		t.Error("i-2 missing from batch")
	}
}

func TestSchedFlushSink_EmptyAndAllUnresolvedSkipReport(t *testing.T) {
	rep := &fakeReporter{}
	s := newSchedFlushSink(fakeResolver{}, rep, testLogger())

	// Empty buffer → no call.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("empty Flush: %v", err)
	}
	// Buffered but nothing resolves → still no call (schedd would reject anyway).
	s.Touch("10.0.0.9:8080", time.Now())
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("unresolved Flush: %v", err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if rep.calls != 0 {
		t.Errorf("ReportActivity calls = %d, want 0", rep.calls)
	}
}

func TestSchedFlushSink_ClearsBufferAndSurfacesError(t *testing.T) {
	resolve := fakeResolver{"10.0.0.2:8080": "i-1"}
	rep := &fakeReporter{err: errors.New("schedd down")}
	s := newSchedFlushSink(resolve, rep, testLogger())

	s.Touch("10.0.0.2:8080", time.Now())
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("expected report error to surface")
	}
	// Buffer was drained up front, so a redelivery doesn't double-count: the
	// next flush has nothing and makes no call.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if rep.calls != 1 {
		t.Errorf("ReportActivity calls = %d, want 1 (buffer cleared after first)", rep.calls)
	}
}

func TestSchedFlushSink_GetForget(t *testing.T) {
	s := newSchedFlushSink(fakeResolver{}, &fakeReporter{}, testLogger())
	now := time.Now()
	s.Touch("a", now)
	if got, ok := s.Get("a"); !ok || !got.Equal(now) {
		t.Errorf("Get = %v,%v", got, ok)
	}
	s.Forget("a")
	if _, ok := s.Get("a"); ok {
		t.Error("addr survived Forget")
	}
}

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

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

// TestSchedFlushSink_KeysByInstanceID (issue #168) — the sink's key is now
// the instance id directly (the row PK schedd owns), so no resolver hop is
// needed on the gateway side. Multiple instances can share a single node;
// their touches are still kept distinct.
func TestSchedFlushSink_KeysByInstanceID(t *testing.T) {
	rep := &fakeReporter{}
	s := newSchedFlushSink(rep, testLogger())

	t0 := time.UnixMilli(1_700_000_000_000)
	s.Touch("i-1", t0)
	s.Touch("i-1", t0.Add(2*time.Second)) // newer wins
	s.Touch("i-2", t0)
	s.Touch("i-3", t0) // unknown to schedd — schedd drops it on its side

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
	if len(byID) != 3 {
		t.Fatalf("touches = %+v, want 3 (i-1, i-2, i-3)", rep.last)
	}
	if !byID["i-1"].Equal(t0.Add(2 * time.Second)) {
		t.Errorf("i-1 time = %v, want newest %v", byID["i-1"], t0.Add(2*time.Second))
	}
	if _, ok := byID["i-2"]; !ok {
		t.Error("i-2 missing from batch")
	}
}

// TestSchedFlushSink_EmptyBufferSkipsReport (issue #168) — no resolver hop
// means the "unresolved" gate is gone; the sink only short-circuits when
// its own buffer is empty.
func TestSchedFlushSink_EmptyBufferSkipsReport(t *testing.T) {
	rep := &fakeReporter{}
	s := newSchedFlushSink(rep, testLogger())

	// Empty buffer → no call.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("empty Flush: %v", err)
	}
	s.Touch("i-9", time.Now())
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("populated Flush: %v", err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if rep.calls != 1 {
		t.Errorf("ReportActivity calls = %d, want 1 (one populated flush)", rep.calls)
	}
}

// TestSchedFlushSink_ClearsBufferAndSurfacesError (issue #168) — flush
// error surfaces to the caller; buffer is drained up front so a retry
// doesn't double-count.
func TestSchedFlushSink_ClearsBufferAndSurfacesError(t *testing.T) {
	rep := &fakeReporter{err: errors.New("schedd down")}
	s := newSchedFlushSink(rep, testLogger())

	s.Touch("i-1", time.Now())
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("expected report error to surface")
	}
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
	s := newSchedFlushSink(&fakeReporter{}, testLogger())
	now := time.Now()
	s.Touch("i-1", now)
	if got, ok := s.Get("i-1"); !ok || !got.Equal(now) {
		t.Errorf("Get = %v,%v", got, ok)
	}
	s.Forget("i-1")
	if _, ok := s.Get("i-1"); ok {
		t.Error("instance id survived Forget")
	}
}

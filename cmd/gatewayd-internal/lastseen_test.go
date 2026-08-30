package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
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

// fakeScheddClientAdapter wraps fakeReporter as a scheddgrpc.ScheddClient so
// the resolver interface can be satisfied without a real gRPC dial. Only the
// ReportActivity method is exercised by the flush sink; the other ScheddClient
// methods panic if invoked (the test seam asserts they're not called from this
// path).
type fakeScheddClientAdapter struct {
	rep *fakeReporter
}

func (a *fakeScheddClientAdapter) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	return a.rep.ReportActivity(ctx, touches)
}

func (a *fakeScheddClientAdapter) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	panic("fakeScheddClientAdapter.AdmitInstance: not wired")
}
func (a *fakeScheddClientAdapter) AdmitMirrorInstance(context.Context, string, string, string) (string, string, error) {
	panic("fakeScheddClientAdapter.AdmitMirrorInstance: not wired")
}
func (a *fakeScheddClientAdapter) EnsureWake(context.Context, string, string) (string, string, string, string, int32, int, error) {
	panic("fakeScheddClientAdapter.EnsureWake: not wired")
}
func (a *fakeScheddClientAdapter) Wake(context.Context, string, string, string) (string, string, string, string, int, error) {
	panic("fakeScheddClientAdapter.Wake: not wired")
}
func (a *fakeScheddClientAdapter) ParkInstance(context.Context, string, string, string) error {
	panic("fakeScheddClientAdapter.ParkInstance: not wired")
}
func (a *fakeScheddClientAdapter) StreamAppLogs(context.Context, string, int64, time.Time, string, string, string) (scheddgrpc.LogStream, error) {
	panic("fakeScheddClientAdapter.StreamAppLogs: not wired")
}
func (a *fakeScheddClientAdapter) StreamWarmHints(context.Context) (scheddgrpc.WarmHintStream, error) {
	panic("fakeScheddClientAdapter.StreamWarmHints: not wired")
}
func (a *fakeScheddClientAdapter) Close() error { return nil }

// fakeResolver is the whitebox seam for scheddInstanceResolver.
// It returns a single ScheddClient for every instance id, which is
// the single-box default-local shape. Tests that want multi-node
// dispatch override `perInstance`.
type fakeResolver struct {
	cli         scheddgrpc.ScheddClient
	perInstance func(instanceID string) scheddgrpc.ScheddClient
}

func (f *fakeResolver) ScheddForInstance(_ context.Context, instanceID string) (scheddgrpc.ScheddClient, error) {
	if f.perInstance != nil {
		if c := f.perInstance(instanceID); c != nil {
			return c, nil
		}
	}
	if f.cli == nil {
		return nil, nil
	}
	return f.cli, nil
}

func newSingleClientResolver(rep *fakeReporter) *fakeResolver {
	return &fakeResolver{cli: &fakeScheddClientAdapter{rep: rep}}
}

// TestSchedFlushSink_KeysByInstanceID (issue #168) — the sink's key is now
// the instance id directly (the row PK schedd owns), so no resolver hop is
// needed on the gateway side. Multiple instances can share a single node;
// their touches are still kept distinct.
func TestSchedFlushSink_KeysByInstanceID(t *testing.T) {
	rep := &fakeReporter{}
	s := newSchedFlushSink(newSingleClientResolver(rep), nil, testLogger())

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
	byDelta := map[string]int64{}
	for _, tc := range rep.last {
		byID[tc.InstanceID] = tc.LastRequest
		byDelta[tc.InstanceID] = tc.RequestDelta
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
	if got := byDelta["i-1"]; got != 2 {
		t.Errorf("i-1 request delta = %d, want 2", got)
	}
	if got := byDelta["i-2"]; got != 1 {
		t.Errorf("i-2 request delta = %d, want 1", got)
	}
}

// TestSchedFlushSink_EmptyBufferSkipsReport (issue #168) — no resolver hop
// means the "unresolved" gate is gone; the sink only short-circuits when
// its own buffer is empty.
func TestSchedFlushSink_EmptyBufferSkipsReport(t *testing.T) {
	rep := &fakeReporter{}
	s := newSchedFlushSink(newSingleClientResolver(rep), nil, testLogger())

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
	s := newSchedFlushSink(newSingleClientResolver(rep), nil, testLogger())

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
	s := newSchedFlushSink(newSingleClientResolver(&fakeReporter{}), nil, testLogger())
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

// TestSchedFlushSink_PartitionsByInstance (Phase 2 / Gate A) — touches for
// instances on different owner nodes land on different per-node clients.
// One Flush issues N ReportActivity calls, one per resolved client. A
// failure on one client does not abort the other.
func TestSchedFlushSink_PartitionsByInstance(t *testing.T) {
	repA := &fakeReporter{}
	repB := &fakeReporter{}
	cliA := &fakeScheddClientAdapter{rep: repA}
	cliB := &fakeScheddClientAdapter{rep: repB}
	resolver := &fakeResolver{
		perInstance: func(id string) scheddgrpc.ScheddClient {
			switch id {
			case "i-a1", "i-a2":
				return cliA
			case "i-b1":
				return cliB
			}
			return nil
		},
	}
	s := newSchedFlushSink(resolver, nil, testLogger())

	t0 := time.UnixMilli(1_700_000_000_000)
	s.Touch("i-a1", t0)
	s.Touch("i-a2", t0.Add(time.Second))
	s.Touch("i-b1", t0.Add(2*time.Second))

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	repA.mu.Lock()
	defer repA.mu.Unlock()
	if repA.calls != 1 {
		t.Errorf("repA.calls = %d, want 1", repA.calls)
	}
	if got := len(repA.last); got != 2 {
		t.Errorf("repA batch = %d, want 2 (i-a1, i-a2)", got)
	}
	repB.mu.Lock()
	defer repB.mu.Unlock()
	if repB.calls != 1 {
		t.Errorf("repB.calls = %d, want 1", repB.calls)
	}
	if got := len(repB.last); got != 1 {
		t.Errorf("repB batch = %d, want 1 (i-b1)", got)
	}
}

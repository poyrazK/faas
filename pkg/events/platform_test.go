package events

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// silentLog discards slog output so test runs stay clean.
func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPlatform_Emit_StoresRow — the AppendEvent path lands the
// events row in the underlying store. Mirrors the
// pkg/audit/audit_test.go::TestAuditor_Emit_WritesRowWithActor
// shape.
func TestPlatform_Emit_StoresRow(t *testing.T) {
	store := newStubStore()
	ops := newStubOps()
	bc := &stubBroadcaster{}
	p := NewPlatform("schedd", store, silentLog(), ops, bc)

	before := time.Now().UTC()
	ev := QueueAccepted{
		EmitAt:    time.Now(),
		WakeID:    "w-1",
		AppID:     "a-1",
		RequestID: "r-1",
	}
	p.Emit(context.Background(), ev)
	after := time.Now().UTC()

	// The row is keyed by subject=wake_id's owning account — but
	// QueueAccepted.Subject() returns nil, so the row is
	// system-level (subject=null). Use ListEvents("", 0) to fetch
	// the bare-nil-subject rows.
	rows, err := store.ListEvents(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Got %d rows, want 1", len(rows))
	}
	if rows[0].Actor != "schedd" {
		t.Errorf("Actor = %q, want schedd", rows[0].Actor)
	}
	if rows[0].Kind != WakeQueueAccepted {
		t.Errorf("Kind = %q, want %q", rows[0].Kind, WakeQueueAccepted)
	}
	if rows[0].Subject != nil {
		t.Errorf("Subject = %v, want nil", *rows[0].Subject)
	}
	// The timestamp surface is the events.at column; we set
	// EmitAt before/after the call so the assertion is bounded.
	if rows[0].At.Before(before) || rows[0].At.After(after) {
		t.Errorf("At = %v, not in [%v, %v]", rows[0].At, before, after)
	}

	// Counter + histogram both fired under the (phase, result=ok)
	// tuple.
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.emittedCalls) != 1 || ops.emittedCalls[0] != "queue_accepted:ok" {
		t.Errorf("emittedCalls = %v, want [queue_accepted:ok]", ops.emittedCalls)
	}
	if len(ops.durationCalls) != 1 || ops.durationCalls[0] != "queue_accepted:ok" {
		t.Errorf("durationCalls = %v, want [queue_accepted:ok]", ops.durationCalls)
	}
	if len(ops.durationSecs) != 1 {
		t.Errorf("durationSecs = %v, want 1", ops.durationSecs)
	}

	// Pub/sub envelope has the wake topic + JSON envelope.
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.calls != 1 {
		t.Errorf("PublishTopic calls = %d, want 1", bc.calls)
	}
	if bc.lastTopic != TopicWake {
		t.Errorf("lastTopic = %q, want %q", bc.lastTopic, TopicWake)
	}
}

// TestPlatform_Emit_FailurePath — a failing AppendEvent does not
// panic; the counter increments under result="failed" and the
// pub/sub publish is skipped (the row never landed). Mirrors
// pkg/audit/audit_test.go::TestAuditor_Emit_FailurePath.
func TestPlatform_Emit_FailurePath(t *testing.T) {
	store := failingStore{state.NewMemStore()}
	ops := newStubOps()
	bc := &stubBroadcaster{}
	p := NewPlatform("schedd", store, silentLog(), ops, bc)

	ev := Admitted{
		EmitAt:    time.Now(),
		WakeID:    "w-1",
		AppID:     "a-1",
		AccountID: "acct-1",
		Plan:      "hobby",
	}
	p.Emit(context.Background(), ev)

	// Counter fires under (phase=admitted, result=failed); the
	// histogram observes the failure-path duration.
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.emittedCalls) != 1 || ops.emittedCalls[0] != "admitted:failed" {
		t.Errorf("emittedCalls = %v, want [admitted:failed]", ops.emittedCalls)
	}
	if len(ops.durationCalls) != 1 || ops.durationCalls[0] != "admitted:failed" {
		t.Errorf("durationCalls = %v, want [admitted:failed]", ops.durationCalls)
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.calls != 0 {
		t.Errorf("PublishTopic fired on failed row; calls = %d, want 0", bc.calls)
	}
}

// TestPlatform_Emit_NilOpsBroadcaster — the helper tolerates
// nil ops and nil broadcaster so platform-less unit tests (e.g.
// schedd engine tests without an OpsMetrics) run without
// panicking. Same nil-safe posture as pkg/audit.Auditor.
func TestPlatform_Emit_NilOpsBroadcaster(t *testing.T) {
	store := newStubStore()
	p := NewPlatform("schedd", store, silentLog(), nil, nil)
	ev := Readiness200{
		EmitAt:          time.Now(),
		WakeID:          "w-1",
		AppID:           "a-1",
		InstanceID:      "i-1",
		NodeID:          "n-1",
		HealthcheckPath: "/healthz",
		ProbeCount:      1,
		ElapsedMs:       47,
	}
	p.Emit(context.Background(), ev)
	rows, err := store.ListEvents(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
}

// TestPlatform_Emit_NilEvent — defensive: a nil WakeEvent is a
// no-op. Mirrors pkg/audit.Auditor.Emit's footnote ("Emit MUST
// tolerate nil event without panicking").
func TestPlatform_Emit_NilEvent(t *testing.T) {
	store := newStubStore()
	ops := newStubOps()
	p := NewPlatform("schedd", store, silentLog(), ops, nil)
	p.Emit(context.Background(), nil)
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.emittedCalls) != 0 {
		t.Errorf("emittedCalls = %v, want []", ops.emittedCalls)
	}
}

type blockingAtStore struct {
	state.Store
	mem     *state.MemStore
	started chan struct{}
	release chan struct{}
	wroteAt chan time.Time
}

func (s *blockingAtStore) AppendEventAt(ctx context.Context, actor, kind string, subject *string, data []byte, at time.Time) error {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := s.mem.AppendEventAt(ctx, actor, kind, subject, data, at); err != nil {
		return err
	}
	s.wroteAt <- at
	return nil
}

func TestPlatform_EmitAsync_DoesNotBlockAndPreservesBoundaryTime(t *testing.T) {
	mem := state.NewMemStore()
	store := &blockingAtStore{
		Store:   mem,
		mem:     mem,
		started: make(chan struct{}),
		release: make(chan struct{}),
		wroteAt: make(chan time.Time, 1),
	}
	p := NewPlatform("schedd", store, silentLog(), nil, nil)
	emitAt := time.Date(2026, time.September, 8, 7, 9, 9, 359000000, time.UTC)

	returned := make(chan struct{})
	go func() {
		p.EmitAsync(context.Background(), QueueAccepted{
			EmitAt: emitAt,
			WakeID: "w-async",
			AppID:  "a-async",
		})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("EmitAsync blocked on the events store")
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("async events worker did not start the write")
	}
	close(store.release)
	select {
	case got := <-store.wroteAt:
		if !got.Equal(emitAt) {
			t.Fatalf("persisted at = %v, want event boundary %v", got, emitAt)
		}
	case <-time.After(time.Second):
		t.Fatal("async events worker did not finish the write")
	}

	rows, err := mem.ListEventsByWakeID(context.Background(), "w-async", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	if len(rows) != 1 || !rows[0].At.Equal(emitAt) {
		t.Fatalf("rows = %+v, want one row at %v", rows, emitAt)
	}
}

// TestPlatform_Actor — the constructor enforces the actor name.
func TestPlatform_Actor(t *testing.T) {
	p := NewPlatform("vmmd", newStubStore(), silentLog(), nil, nil)
	if got := p.Actor(); got != "vmmd" {
		t.Errorf("Actor = %q, want vmmd", got)
	}
}

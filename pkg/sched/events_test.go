package sched

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// uuidStringOf normalises either a canonical UUID (with hyphens) or a
// raw 32-char hex string into the canonical UUID form. The MemStore's
// newID returns hex; the PgStore returns canonical UUIDs. MemStore's
// parseSubjectID (see pkg/state/memstore.go) converts the hex back to
// canonical UUID bytes when storing the Subject, so an events row's
// Subject.String() always returns the canonical form regardless of
// which store produced it.
func uuidStringOf(s string) string {
	if strings.Contains(s, "-") {
		return s
	}
	if len(s) != 32 {
		return s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return s
	}
	return uuid.UUID(b).String()
}

// TestEngineTransition_AppendsEvent (commit 4) drives a happy-path
// Wake and asserts the events table got a row for the RUNNING
// transition. The init state (cold_booting) is a CreateInstance,
// not a transition, so it's not expected to emit an event row here.
// The single row we DO expect goes through the default `transition`
// path which emits kind="state_transition" with from/to/reason/ts.
func TestEngineTransition_AppendsEvent(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}

	rows, err := store.ListEvents(context.Background(), res.InstanceID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) < 1 {
		t.Fatalf("ListEvents returned %d rows, want ≥1 (RUNNING transition); rows=%+v", len(rows), rows)
	}
	// The row we have must be the happy-path transition.
	for _, e := range rows {
		if e.Kind != "state_transition" {
			t.Errorf("event kind = %q, want %q (happy path)", e.Kind, "state_transition")
		}
		if e.Subject == nil {
			t.Errorf("event Subject = nil; commit 4's MemStore fix should set it")
		} else if e.Subject.String() != uuidStringOf(res.InstanceID) {
			t.Errorf("event Subject = %s, want %s", *e.Subject, uuidStringOf(res.InstanceID))
		}
		if !json.Valid(e.Data) {
			t.Errorf("event Data = %q, want valid JSON", e.Data)
		}
	}
	// And the payload shape: from=cold_booting, to=running.
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Data, &payload); err != nil {
		t.Fatalf("event Data not valid JSON: %v", err)
	}
	if payload["from"] != "cold_booting" {
		t.Errorf("event Data from = %v, want cold_booting", payload["from"])
	}
	if payload["to"] != "running" {
		t.Errorf("event Data to = %v, want running", payload["to"])
	}
}

// TestEngineTransition_EventWriteFailureDoesNotRollback (commit 4)
// proves the audit-log emission is best-effort. Inject a fake store
// whose AppendEvent always errors; run a happy Wake; expect the
// transition to still complete (row RUNNING, no error returned) and
// the events_write_failures counter to increment by one.
//
// We can't easily wrap the MemStore to make AppendEvent fail, so we
// build a thin wrapper struct that delegates everything else and
// returns an error on AppendEvent only.
func TestEngineTransition_EventWriteFailureDoesNotRollback(t *testing.T) {
	inner := state.NewMemStore()
	_, app, _ := seedApp(t, inner, api.PlanPro, 512, 5)
	wrapped := &failingEventStore{Store: inner}
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, wrapped, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake returned err (must succeed despite audit failure): %v", err)
	}
	ins, _ := inner.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateRunning) {
		t.Errorf("state = %q, want RUNNING (audit-log failure must not roll back)", ins.State)
	}
	if got := testutil.ToFloat64(ops.EventsWriteFailures()); got < 1 {
		t.Errorf("events_write_failures = %v, want ≥1", got)
	}
}

// TestEngineKillStuck_AppendsEvent (commit 4) drives the watchdog's
// audit kind: kill a stuck WAKING row and assert the events table
// got a row with kind="watchdog_timeout". This is the same row that
// drives TestWatchdogSweepKillsStuck; here we focus on the audit
// shape specifically.
func TestEngineKillStuck_AppendsEvent(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	ins, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := engine.KillStuck(context.Background(), ins.ID, app.ID, StuckWakingTimeout); err != nil {
		t.Fatalf("KillStuck: %v", err)
	}
	rows, err := store.ListEvents(context.Background(), ins.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListEvents returned %d rows, want 1 (watchdog_timeout); rows=%+v", len(rows), rows)
	}
	e := rows[0]
	if e.Kind != "watchdog_timeout" {
		t.Errorf("event kind = %q, want %q", e.Kind, "watchdog_timeout")
	}
	// Data payload must contain from/to/reason keys; check the shape.
	var payload map[string]any
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		t.Fatalf("event Data not valid JSON: %v", err)
	}
	if payload["from"] != "waking" {
		t.Errorf("event Data from = %v, want waking", payload["from"])
	}
	if payload["to"] != "cold_booting" {
		t.Errorf("event Data to = %v, want cold_booting", payload["to"])
	}
	if payload["reason"] != string(StuckWakingTimeout) {
		t.Errorf("event Data reason = %v, want %s", payload["reason"], StuckWakingTimeout)
	}
}

// TestEngineWake_FailedBoot_AppendsWakeBootError drives Wake's
// boot-error path and asserts the events row carries kind =
// "wake_boot_error" (not the default "state_transition"). This is
// the audit-log shape that lets ops query "all wake-boot failures
// in the last hour" without grepping log lines.
func TestEngineWake_FailedBoot_AppendsWakeBootError(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{wakeErr: errBoom}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	_, err := engine.Wake(context.Background(), app.ID)
	if err == nil {
		t.Fatal("expected Wake to fail (vmm.wakeErr set)")
	}
	// Find the FAILED row.
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Fatalf("rows = %+v, want one FAILED", rows)
	}
	events, _ := store.ListEvents(context.Background(), rows[0].ID, 0)
	if len(events) == 0 {
		t.Fatal("ListEvents empty; expected at least one row with kind=wake_boot_error")
	}
	var found bool
	for _, e := range events {
		if e.Kind == "wake_boot_error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("events rows = %+v, want one with kind=wake_boot_error", events)
	}
}

// errBoom is a sentinel error for tests. Defining it here keeps the
// errs surface local to events_test.
var errBoom = boomErr("boom")

type boomErr string

func (e boomErr) Error() string { return string(e) }

// TestEnginePark_SnapshotFail_AppendsParkSnapshotError drives
// snapshotAndPark's error path and asserts the events row carries
// kind = "park_snapshot_error" (per the kind taxonomy in
// transitionWithKind's doc). This is the audit shape that lets ops
// query "all park-snapshot failures in the last hour" without
// grepping log lines.
func TestEnginePark_SnapshotFail_AppendsParkSnapshotError(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{snapErr: errBoom}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := engine.Park(context.Background(), res.InstanceID); err == nil {
		t.Fatal("expected Park to fail (vmm.snapErr set)")
	}
	events, _ := store.ListEvents(context.Background(), res.InstanceID, 0)
	var found bool
	for _, e := range events {
		if e.Kind == "park_snapshot_error" {
			found = true
			var payload map[string]any
			if jerr := json.Unmarshal(e.Data, &payload); jerr != nil {
				t.Errorf("park_snapshot_error Data not valid JSON: %v", jerr)
			}
			if payload["from"] != "snapshotting" {
				t.Errorf("park_snapshot_error Data from = %v, want snapshotting", payload["from"])
			}
			if payload["to"] != "stopped" {
				t.Errorf("park_snapshot_error Data to = %v, want stopped", payload["to"])
			}
			if payload["reason"] != "snapshot_failed" {
				t.Errorf("park_snapshot_error Data reason = %v, want snapshot_failed", payload["reason"])
			}
			break
		}
	}
	if !found {
		t.Errorf("events rows = %+v, want one with kind=park_snapshot_error", events)
	}
}

// TestMemStoreAppendEvent_HexSubjectRoundTrips (commit 4 fix) proves
// that a MemStore instance whose ID is the 32-char hex form
// (newID()'s output) survives the audit-log round-trip. Before the
// parseSubjectID fix, MemStore.AppendEvent silently dropped the
// Subject on hex-only inputs, so ListEvents(subject=<hex>) returned
// no rows even though AppendEvent had just been called with that
// exact subject string.
func TestMemStoreAppendEvent_HexSubjectRoundTrips(t *testing.T) {
	store := state.NewMemStore()
	ins, err := store.CreateInstance(context.Background(), "app", "dep", string(state.StateWaking), 256, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	// Hex-only ID is the production shape for MemStore instances.
	if strings.Contains(ins.ID, "-") {
		t.Fatalf("MemStore created a non-hex ID %q — fix is unnecessary", ins.ID)
	}

	subject := ins.ID
	data := []byte(`{"k":"v"}`)
	if err := store.AppendEvent(context.Background(), "schedd", "state_transition", &subject, data); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	rows, err := store.ListEvents(context.Background(), ins.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListEvents(%q) returned %d rows, want 1", ins.ID, len(rows))
	}
	if rows[0].Subject == nil {
		t.Fatal("Subject = nil after AppendEvent; parseSubjectID fix should have set it")
	}
	if rows[0].Subject.String() != uuidStringOf(ins.ID) {
		t.Errorf("Subject = %s, want %s", rows[0].Subject, uuidStringOf(ins.ID))
	}
}

// failingEventStore is a state.Store wrapper that returns an error
// from AppendEvent only. It exists so commit 4's tests can prove the
// transition is best-effort: an AppendEvent failure must NOT roll
// back the state write.
type failingEventStore struct {
	state.Store
	failures int64
}

func (f *failingEventStore) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	f.failures++
	return boomErr("simulated AppendEvent failure")
}

// recreate_test.go — coverage for Engine.RecreateInstance
// (Workstream B / issue #1184 / ADR-137). The primitive lives
// behind the recovery arbiter (Task #59); these tests pin the
// per-instance verdict contract so a future engine field
// addition can't silently bypass the CAS.
// adr: 137

package sched

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// recreateTestEngine is a minimal Engine wired for the recreate
// primitive's surface. We bypass NewEngine (which requires a
// VMMClient + Ledger + Notifier) by constructing the bare fields
// the primitive touches: store, ledger, ops, events, log.
// ledger / events / ops are nil — the primitive tolerates nil
// in each case (Task #59 test pattern: nil-dispatcher tolerance
// is load-bearing for the bootstrap window).
func recreateTestEngine(t *testing.T) (*Engine, *state.MemStore) {
	t.Helper()
	store := state.NewMemStore()
	return &Engine{
		store: store,
		log:   slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, store
}

// seedInstanceForRecreate inserts a live-instance row the
// recreate primitive can transition. CreateInstance returns the
// minted Instance (with its ID) so the test can return it
// directly without a follow-up lookup.
func seedInstanceForRecreate(t *testing.T, store *state.MemStore, stateStr string) state.Instance {
	t.Helper()
	ins, err := store.CreateInstance(context.Background(),
		"app-1", "dep-1", stateStr, 256, "node-a", "wake-x")
	if err != nil {
		t.Fatalf("seed %s: %v", stateStr, err)
	}
	return ins
}

// TestRecreateInstance_HappyPath — RUNNING row → PARKED.
// Confirms the transition lands and no error is returned.
func TestRecreateInstance_HappyPath(t *testing.T) {
	e, store := recreateTestEngine(t)
	ins := seedInstanceForRecreate(t, store, "running")

	if err := e.RecreateInstance(context.Background(), ins.ID); err != nil {
		t.Fatalf("RecreateInstance: %v", err)
	}
	post, err := store.InstanceByID(context.Background(), ins.ID)
	if err != nil {
		t.Fatalf("post-load: %v", err)
	}
	if post.State != "parked" {
		t.Errorf("State = %q, want parked", post.State)
	}
}

// TestRecreateInstance_ServiceReplicaReconcilesDeficit pins the recovery
// contract for long-running services: parking a stranded replica must wake a
// replacement so desired capacity is restored without waiting for another
// lifecycle notification.
func TestRecreateInstance_ServiceReplicaReconcilesDeficit(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 4)
	manifest := state.AppManifest{
		ExecutionMode: api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{
			Min: 1, Max: 2, Desired: 2,
		},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
			string(state.StateRunning), app.RAMMB, "node-a", "service-recovery-"+string(rune('a'+i)), string(state.InstanceModeService)); err != nil {
			t.Fatalf("CreateInstanceWithMode[%d]: %v", i, err)
		}
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("seeded instances = %d, want 2", len(instances))
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.RecreateInstance(context.Background(), instances[0].ID); err != nil {
		t.Fatalf("RecreateInstance: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, listErr := store.ListInstancesForApp(context.Background(), app.ID)
		if listErr != nil {
			t.Fatalf("ListInstancesForApp after recovery: %v", listErr)
		}
		running, parked := 0, 0
		for _, row := range rows {
			if row.Mode != string(state.InstanceModeService) {
				continue
			}
			switch state.State(row.State) {
			case state.StateRunning:
				running++
			case state.StateParked:
				parked++
			}
		}
		if running == 2 && parked == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("service replicas did not reconverge: want running=2 parked=1, got rows=%+v", instances)
}

// TestRecreateInstance_SkipsTerminalStates — STOPPED and FAILED
// rows are out of scope; the primitive returns nil without
// touching the row. Guards against accidentally re-allocating
// a cold-boot from a row that already served its purpose.
func TestRecreateInstance_SkipsTerminalStates(t *testing.T) {
	cases := []string{"stopped", "failed"}
	for _, st := range cases {
		st := st
		t.Run(st, func(t *testing.T) {
			e, store := recreateTestEngine(t)
			ins := seedInstanceForRecreate(t, store, st)
			if err := e.RecreateInstance(context.Background(), ins.ID); err != nil {
				t.Fatalf("RecreateInstance(%s): %v", st, err)
			}
			post, _ := store.InstanceByID(context.Background(), ins.ID)
			if post.State != st {
				t.Errorf("State changed: %q → %q (primitive must skip terminal rows)", st, post.State)
			}
		})
	}
}

// TestRecreateInstance_NotFound — ErrNotFound from the store
// resolves to nil (peer-wins) rather than a hard error so the
// arbiter's per-tick loop counts the dispatch as a no-op.
func TestRecreateInstance_NotFound(t *testing.T) {
	e, _ := recreateTestEngine(t)
	if err := e.RecreateInstance(context.Background(), "missing-id"); err != nil {
		t.Errorf("RecreateInstance(missing) = %v; want nil", err)
	}
}

// TestRecreateInstance_NilEngine — calling on a nil *Engine
// is a no-op (defensive; the arbiter construction window can
// momentarily hold a nil engine pointer before wiring lands).
func TestRecreateInstance_NilEngine(t *testing.T) {
	var e *Engine
	if err := e.RecreateInstance(context.Background(), "any"); err != nil {
		t.Errorf("nil-engine RecreateInstance = %v; want nil", err)
	}
}

// TestRecreateInstance_StateOutOfScope — PARKED rows skip.
// The arbiter is only supposed to fire on RUNNING / COLD_BOOTING
// / WAKING; PARKED is the rebalancer's territory.
func TestRecreateInstance_StateOutOfScope(t *testing.T) {
	e, store := recreateTestEngine(t)
	ins := seedInstanceForRecreate(t, store, "parked")
	if err := e.RecreateInstance(context.Background(), ins.ID); err != nil {
		t.Fatalf("RecreateInstance(parked): %v", err)
	}
	post, _ := store.InstanceByID(context.Background(), ins.ID)
	if post.State != "parked" {
		t.Errorf("State changed: parked → %q", post.State)
	}
}

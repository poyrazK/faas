// recreate_test.go — coverage for Engine.RecreateInstance
// (Workstream B / issue #1184 / ADR-137). The primitive lives
// behind the recovery arbiter (Task #59); these tests pin the
// per-instance verdict contract so a future engine field
// addition can't silently bypass the CAS.
package sched

import (
	"context"
	"log/slog"
	"os"
	"testing"

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

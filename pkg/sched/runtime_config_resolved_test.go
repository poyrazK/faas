// adr: 210
// adr: 1523
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt #3: `secrets set --restart` admitted a cold boot, `secrets unset`
// landed during the boot, and the boot still delivered the deleted secret
// (sealed at admission). Its started_at (readiness) came after the delete, so
// the restart path's init capture passed the freshness guard and every later
// restore restored the deleted secret. Freshness must be anchored on when the
// configuration was resolved: the wake's admission.
func TestRuntimeConfigStaleAnchorsOnWakeAdmission(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deployment := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

	admittedWake, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	readyAfterChange := time.Now()

	booting := resolvedConfigInstance(t, store, app, deployment, admittedWake.String(), readyAfterChange)
	if !e.RuntimeConfigStale(ctx, booting) {
		t.Fatal("instance whose wake was admitted before the change was judged fresh; its capture would publish the old secrets")
	}

	time.Sleep(5 * time.Millisecond)
	laterWake, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	fresh := resolvedConfigInstance(t, store, app, deployment, laterWake.String(), time.Now())
	if e.RuntimeConfigStale(ctx, fresh) {
		t.Fatal("instance admitted after the change was judged stale")
	}

	// Legacy or non-v7 wake ids fall back to started_at.
	legacy := resolvedConfigInstance(t, store, app, deployment, uuid.NewString(), readyAfterChange)
	if e.RuntimeConfigStale(ctx, legacy) {
		t.Fatal("non-v7 wake id must fall back to started_at")
	}
}

// adr: 590
func TestStageSnapshotFreshnessKeepsAdmissionClockAndEnvironmentOwner(t *testing.T) {
	ctx := t.Context()
	f := sched.SeedStageSnapshotPolicy(t, 0, false)
	e := sched.XNewEngine(t, (*f.Store()), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	admitted, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if err := (*f.Store()).MarkAppRuntimeConfigChanged(ctx, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	ins := resolvedConfigInstance(t, (*f.Store()), (*f.App()), (*f.Stage()), admitted.String(), time.Now())
	if !e.RuntimeConfigStale(ctx, ins) {
		t.Fatal("stage capture revived configuration removed after admission")
	}
	// Legacy admission IDs use readiness, while the deployment must still
	// retain its environment ownership for snapshot publication.
	ins = resolvedConfigInstance(t, (*f.Store()), (*f.App()), (*f.Stage()), uuid.NewString(), time.Now())
	if e.RuntimeConfigStale(ctx, ins) {
		t.Fatal("fresh owned stage configuration rejected")
	}
	f.RecreateStage(ctx, t)
	ins.StartedAt = time.Now() // The clock is fresh; only the retired owner can reject it.
	if !e.RuntimeConfigStale(ctx, ins) {
		t.Fatal("recreated stage accepted a snapshot from its retired owner")
	}
}

func resolvedConfigInstance(t *testing.T, store *state.MemStore, app state.App, deployment state.Deployment, wakeID string, readyAt time.Time) state.Instance {
	t.Helper()
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(t.Context(), app.ID, deployment.ID, string(state.StateRunning), app.RAMMB, node.ID, wakeID)
	if err != nil {
		t.Fatal(err)
	}
	ins.StartedAt = readyAt
	return ins
}

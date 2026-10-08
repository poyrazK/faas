package sched

// adr: 470 — a failed wake records its terminal state even after the caller's
// deadline expired.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ctxStore refuses work on a finished context, as PgStore does.
type ctxStore struct{ state.Store }

func (s ctxStore) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	if err := ctx.Err(); err != nil {
		return state.Instance{}, err
	}
	return s.Store.InstanceByID(ctx, id)
}

func (s ctxStore) UpdateInstanceStateToTerminal(ctx context.Context, id, st string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.UpdateInstanceStateToTerminal(ctx, id, st, at)
}

// production-us 2026-10-07: a restore fallback outlived the wake caller's
// deadline, vmmd returned "context canceled", and the FAILED write ran on the
// same expired context. The row stayed live until the watchdog swept it.
func TestWakeBootFailureRecordsFailedAfterCallerDeadline(t *testing.T) {
	base := state.NewMemStore()
	_, app, _ := seedApp(t, base, api.PlanPro, 512, 5)
	vmm := &fakeVMM{sleepFor: 5 * time.Second}
	e := newEngine(t, ctxStore{Store: base}, vmm, &fakeNotifier{}, "1.10.0")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err == nil {
		t.Fatal("wake succeeded past the caller's deadline")
	}
	instances, err := base.ListInstancesForApp(context.Background(), app.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances = %v err=%v, want the one failed wake", instances, err)
	}
	if got := instances[0].State; got != string(state.StateFailed) {
		t.Fatalf("instance state = %s, want failed", got)
	}
}

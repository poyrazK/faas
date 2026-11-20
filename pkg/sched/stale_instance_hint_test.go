package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type statsFunc = func(context.Context, string) (*sched.StatsSnapshot, error)

func TestReconcileStaleInstanceHint(t *testing.T) {
	// Seconds, not the sweep's 60 s grace: a replacement that booted moments
	// before its owner crashed must still be judged.
	past := 10 * time.Second
	empty := func(string) statsFunc {
		return func(context.Context, string) (*sched.StatsSnapshot, error) { return &sched.StatsSnapshot{}, nil }
	}
	cases := []struct {
		name       string
		stats      func(id string) statsFunc
		afterStart time.Duration
		otherApp   bool
		wantMoved  bool
		wantState  state.State
	}{
		{name: "vmmd answers without the VM: row fails", stats: empty, afterStart: past, wantMoved: true, wantState: state.StateFailed},
		{
			name: "vmmd lists the VM: row stays",
			stats: func(id string) statsFunc {
				return func(context.Context, string) (*sched.StatsSnapshot, error) {
					return &sched.StatsSnapshot{Instances: []sched.VMInstanceStat{{InstanceID: id}}}, nil
				}
			},
			afterStart: past, wantState: state.StateRunning,
		},
		{
			name: "vmmd unreachable: no evidence, row stays",
			stats: func(string) statsFunc {
				return func(context.Context, string) (*sched.StatsSnapshot, error) { return nil, errors.New("unavailable") }
			},
			afterStart: past, wantState: state.StateRunning,
		},
		{name: "inside the grace window: row stays", stats: empty, afterStart: 500 * time.Millisecond, wantState: state.StateRunning},
		{name: "hint for another app: row stays", stats: empty, afterStart: past, otherApp: true, wantState: state.StateRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanHobby, 256, 5)
			vmm := &sched.FakeVMM{}
			engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, "node-a", "")
			if err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
			*vmm.StatsHook() = tc.stats(ins.ID)
			start := ins.StartedAt
			if start.IsZero() {
				start = time.Now()
			}
			now := start.Add(tc.afterStart)
			engine.XSetNow(func() time.Time { return now })

			appID := app.ID
			if tc.otherApp {
				appID = "some-other-app"
			}
			if got := engine.ReconcileStaleInstanceHint(ctx, appID, ins.ID); got != tc.wantMoved {
				t.Fatalf("moved=%v want %v", got, tc.wantMoved)
			}
			after, err := store.InstanceByID(ctx, ins.ID)
			if err != nil {
				t.Fatalf("InstanceByID: %v", err)
			}
			if after.State != string(tc.wantState) {
				t.Fatalf("state=%s want %s", after.State, tc.wantState)
			}
		})
	}
}

func TestStaleInstanceHintContext(t *testing.T) {
	ctx := context.Background()
	if got := sched.StaleInstanceHintFromContext(ctx); got != "" {
		t.Fatalf("empty ctx hint=%q", got)
	}
	if got := sched.StaleInstanceHintFromContext(sched.WithStaleInstanceHint(ctx, "")); got != "" {
		t.Fatalf("empty id hint=%q", got)
	}
	if got := sched.StaleInstanceHintFromContext(sched.WithStaleInstanceHint(ctx, "ins-1")); got != "ins-1" {
		t.Fatalf("hint=%q want ins-1", got)
	}
}

// adr: 062 — Amendment 3: the unplaced sweep repeats every minute.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSweepUnplaced pins the backstop for a lost kind=created notify
// (prod 2026-11-25, project 8624ac80): a row left with apps.node_id NULL
// is claimed by the sweep instead of waiting for the next schedd restart.
func TestSweepUnplaced(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ramMB      int
		preplaced  bool
		wantFound  int
		wantFailed int
		wantPlaced bool
	}{
		{name: "unplaced app is claimed", ramMB: 256, wantFound: 1, wantPlaced: true},
		{name: "placed app is left alone", ramMB: 256, preplaced: true, wantFound: 0, wantPlaced: true},
		// 50 GB fits no node: the claim fails, the sweep keeps going and
		// the row stays NULL for the next sweep.
		{name: "no fit stays unplaced for the next sweep", ramMB: 50_000, wantFound: 1, wantFailed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, _ := sched.SeedApp(t, store, api.PlanHobby, tc.ramMB, 1)
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			if tc.preplaced {
				if err := e.ClaimUnplaced(ctx, app.ID); err != nil {
					t.Fatalf("pre-place: %v", err)
				}
			}

			found, failed, err := e.SweepUnplaced(ctx)
			if err != nil {
				t.Fatalf("SweepUnplaced: %v", err)
			}
			if found != tc.wantFound || failed != tc.wantFailed {
				t.Errorf("found, failed = %d, %d; want %d, %d", found, failed, tc.wantFound, tc.wantFailed)
			}
			got, err := store.AppByID(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if placed := got.NodeID != ""; placed != tc.wantPlaced {
				t.Errorf("apps.node_id = %q, want placed=%v", got.NodeID, tc.wantPlaced)
			}
		})
	}
}

// TestRunUnplacedSweep checks that the loop sweeps at start and again on
// its interval, so an app left unplaced after schedd started is still
// claimed, and that it returns when ctx ends.
func TestRunUnplacedSweep(t *testing.T) {
	store := state.NewMemStore()
	acct, first, _ := sched.SeedApp(t, store, api.PlanHobby, 256, 1)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.RunUnplacedSweep(ctx, 20*time.Millisecond)
	}()
	waitPlaced(t, store, first.ID)

	late, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "late-app", RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitPlaced(t, store, late.ID)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunUnplacedSweep did not return after ctx was cancelled")
	}
}

func waitPlaced(t *testing.T, store *state.MemStore, appID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		app, err := store.AppByID(context.Background(), appID)
		if err != nil {
			t.Fatal(err)
		}
		if app.NodeID != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("app %s still unplaced after 5s", appID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

package state_test

// adr: 732
// Scheduler side of production forks: claim order, lease guards, terminal
// rules, TTL sweeps and abandoned-lease takeover, on both stores.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

const forkLease = time.Minute

func createForkAt(t *testing.T, f appForkFixture, at time.Time, ttl int) state.AppFork {
	t.Helper()
	p := f.params(at)
	p.TTLSeconds, p.MaxPerApp, p.MaxPerAccount = ttl, 10, 10
	fork, err := f.store.CreateAppFork(context.Background(), p)
	if err != nil {
		t.Fatalf("CreateAppFork: %v", err)
	}
	return fork
}

func TestAppForkLifecycle_ClaimRunFinish(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			older := createForkAt(t, f, forkT0, 3600)
			createForkAt(t, f, forkT0.Add(time.Second), 3600)

			claimed, err := f.store.ClaimNextAppFork(ctx, "schedd-a", forkT0.Add(time.Minute), forkLease)
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			if claimed.ID != older.ID || claimed.Status != state.AppForkRestoring || claimed.LeaseToken == nil || *claimed.LeaseOwner != "schedd-a" {
				t.Fatalf("claimed = %+v, want the older fork restoring under schedd-a", claimed)
			}
			token := *claimed.LeaseToken

			if _, err := f.store.MarkAppForkRunning(ctx, claimed.ID, "00000000-0000-0000-0000-000000000009", "00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000b1", forkT0.Add(2*time.Minute)); !errors.Is(err, state.ErrAppForkLeaseLost) {
				t.Fatalf("running with a stale token err = %v, want ErrAppForkLeaseLost", err)
			}
			running, err := f.store.MarkAppForkRunning(ctx, claimed.ID, token, "00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000b1", forkT0.Add(2*time.Minute))
			if err != nil || running.Status != state.AppForkRunning || running.InstanceID == nil || running.StartedAt == nil {
				t.Fatalf("running = %+v, %v", running, err)
			}
			if _, err := f.store.RenewAppForkLease(ctx, claimed.ID, token, forkT0.Add(3*time.Minute), forkLease); err != nil {
				t.Fatalf("renew: %v", err)
			}

			done, err := f.store.FinishAppFork(ctx, state.FinishAppForkParams{
				ForkID: claimed.ID, LeaseToken: token, Status: state.AppForkExpired, FinishedAt: forkT0.Add(time.Hour),
			})
			if err != nil || done.Status != state.AppForkExpired || done.LeaseToken != nil || done.FinishedAt == nil {
				t.Fatalf("finish = %+v, %v; want expired with the lease cleared", done, err)
			}
			if _, err := f.store.RenewAppForkLease(ctx, claimed.ID, token, forkT0.Add(time.Hour), forkLease); !errors.Is(err, state.ErrAppForkLeaseLost) {
				t.Fatalf("renew after finish err = %v, want ErrAppForkLeaseLost", err)
			}
		})
	}
}

func TestAppForkLifecycle_FinishRejectsBadTerminalShapes(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			base := state.FinishAppForkParams{ForkID: "f", LeaseToken: "t", FinishedAt: forkT0}
			for label, p := range map[string]state.FinishAppForkParams{
				"non-terminal":      {ForkID: base.ForkID, LeaseToken: base.LeaseToken, FinishedAt: base.FinishedAt, Status: state.AppForkRunning},
				"failed, no code":   {ForkID: base.ForkID, LeaseToken: base.LeaseToken, FinishedAt: base.FinishedAt, Status: state.AppForkFailed},
				"expired with code": {ForkID: base.ForkID, LeaseToken: base.LeaseToken, FinishedAt: base.FinishedAt, Status: state.AppForkExpired, FailureCode: "x", FailureMessage: "y"},
			} {
				if _, err := f.store.FinishAppFork(context.Background(), p); !errors.Is(err, state.ErrAppForkInvalid) {
					t.Errorf("%s: err = %v, want ErrAppForkInvalid", label, err)
				}
			}
		})
	}
}

func TestAppForkLifecycle_ClaimSkipsExpiredAndCancelled(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			expired := createForkAt(t, f, forkT0, 60)
			cancelled := createForkAt(t, f, forkT0, 3600)
			if _, err := f.store.RequestAppForkCancellation(ctx, f.accountID, f.appID, cancelled.ID, forkT0.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			now := forkT0.Add(2 * time.Minute)
			if _, err := f.store.ClaimNextAppFork(ctx, "schedd-a", now, forkLease); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim err = %v, want ErrNotFound (only expired and cancelled forks exist)", err)
			}
			swept, err := f.store.ExpireUnclaimedAppForks(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(swept) != 1 || swept[0].ID != expired.ID || swept[0].Status != state.AppForkExpired {
				t.Fatalf("swept = %+v, want only the expired queued fork (the cancelled one is already terminal)", swept)
			}
		})
	}
}

func TestAppForkLifecycle_TeardownAndTakeover(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fork := createForkAt(t, f, forkT0, 600)
			claimed, err := f.store.ClaimNextAppFork(ctx, "schedd-a", forkT0.Add(time.Second), forkLease)
			if err != nil {
				t.Fatal(err)
			}

			// Not due yet; then due at its TTL, and only for its owner.
			if due, _ := f.store.AppForksDueForTeardown(ctx, "schedd-a", forkT0.Add(time.Minute), 10); len(due) != 0 {
				t.Fatalf("due before TTL = %+v", due)
			}
			if due, _ := f.store.AppForksDueForTeardown(ctx, "schedd-a", forkT0.Add(10*time.Minute), 10); len(due) != 1 || due[0].ID != fork.ID {
				t.Fatalf("due at TTL = %+v, want the fork", due)
			}
			if due, _ := f.store.AppForksDueForTeardown(ctx, "schedd-b", forkT0.Add(10*time.Minute), 10); len(due) != 0 {
				t.Fatalf("another owner sees %+v", due)
			}

			// Lease still live: nothing to take over. Lease lapsed: schedd-b
			// takes it, and schedd-a's token stops working.
			if _, err := f.store.TakeOverAbandonedAppFork(ctx, "schedd-b", forkT0.Add(30*time.Second), forkLease); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("takeover of a live lease err = %v, want ErrNotFound", err)
			}
			taken, err := f.store.TakeOverAbandonedAppFork(ctx, "schedd-b", forkT0.Add(5*time.Minute), forkLease)
			if err != nil || taken.ID != fork.ID || *taken.LeaseOwner != "schedd-b" || *taken.LeaseToken == *claimed.LeaseToken {
				t.Fatalf("takeover = %+v, %v", taken, err)
			}
			if _, err := f.store.RenewAppForkLease(ctx, fork.ID, *claimed.LeaseToken, forkT0.Add(5*time.Minute), forkLease); !errors.Is(err, state.ErrAppForkLeaseLost) {
				t.Fatalf("old owner renew err = %v, want ErrAppForkLeaseLost", err)
			}
			failed, err := f.store.FinishAppFork(ctx, state.FinishAppForkParams{
				ForkID: fork.ID, LeaseToken: *taken.LeaseToken, Status: state.AppForkFailed,
				FailureCode: "scheduler_lost", FailureMessage: "the scheduler holding this fork stopped", FinishedAt: forkT0.Add(5 * time.Minute),
			})
			if err != nil || failed.Status != state.AppForkFailed || failed.FailureCode == nil {
				t.Fatalf("finish failed = %+v, %v", failed, err)
			}
		})
	}
}

// RunningInstanceForApp feeds apid's per-app instance lookups. A fork is
// running on the live deployment but must never be the answer, even when it
// started after the serving instance.
func TestRunningInstanceForAppSkipsForks(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			serving, err := f.store.CreateInstanceWithMode(ctx, f.appID, f.liveDep, string(state.StateRunning), 512, f.nodeID, uuid.NewString(), string(state.InstanceModeNormal))
			if err != nil {
				t.Fatalf("serving instance: %v", err)
			}
			if _, err := f.store.CreateInstanceWithMode(ctx, f.appID, f.liveDep, string(state.StateRunning), 512, f.nodeID, uuid.NewString(), string(state.InstanceModeFork)); err != nil {
				t.Fatalf("fork instance: %v", err)
			}
			got, err := f.store.RunningInstanceForApp(ctx, f.appID)
			if err != nil || got.ID != serving.ID {
				t.Fatalf("RunningInstanceForApp = %s, %v; want the serving instance %s", got.ID, err, serving.ID)
			}
		})
	}
}

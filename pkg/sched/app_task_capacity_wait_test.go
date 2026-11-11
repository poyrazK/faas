package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const capacityWaitNode = "11111111-1111-4111-8111-111111111111"

func capacityRefusingBackend() nodeClaimBackend {
	refusal := sched.NodeBudgetRefusal("placement: no active compute_node fits",
		api.WorkerAdmissionBlocker{Code: "node_vcpu", Resource: "vcpu", Requested: ptrInt64(4)})
	return nodeClaimBackend(func(context.Context, sched.AppTaskRestoreRequest) (sched.AppTaskSession, error) {
		return nil, refusal
	})
}

func processCapacityRefusal(t *testing.T, store *state.MemStore, task state.AppTask) state.AppTask {
	t.Helper()
	processed, err := sched.NewAppTaskCoordinator(store, capacityRefusingBackend(), nodeClaimConfig(capacityWaitNode), nil).
		ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v; want one claim without error", processed, err)
	}
	got, err := store.AppTaskByID(context.Background(), task.AccountID, task.AppID, task.ID)
	if err != nil {
		t.Fatalf("AppTaskByID: %v", err)
	}
	return got
}

func wantCapacityFailure(t *testing.T, got state.AppTask) {
	t.Helper()
	if got.Status != state.AppTaskFailed || got.FailureCode == nil || *got.FailureCode != "capacity_unavailable" {
		t.Fatalf("task = %s (failure %v); want failed with capacity_unavailable", got.Status, got.FailureCode)
	}
}

// A release task whose environment no node had room for failed for good, and
// its deployment with it, though no command had run (production 2026-11-05:
// eight placement refusals on compute-1 within five minutes). schedd now
// returns it to the queue for api.AppTaskCapacityRetryDelay, without a
// failure, so the next claim places it again (ADR-20261029103512 amendment 3).
func TestAppTaskCoordinatorRequeuesReleaseRefusedForCapacity(t *testing.T) {
	store, task := newNodeOwnedAppTaskOfKind(t, capacityWaitNode, state.AppTaskKindRelease)
	before := time.Now().UTC()
	got := processCapacityRefusal(t, store, task)
	if got.Status != state.AppTaskQueued || got.FailureCode != nil || got.LeaseToken != nil {
		t.Fatalf("task = %s (failure %v, lease %v); want queued without failure or lease", got.Status, got.FailureCode, got.LeaseToken)
	}
	if got.RetryAt == nil || got.RetryAt.Before(before.Add(api.AppTaskCapacityRetryDelay)) {
		t.Fatalf("retry_at = %v; want at least %s from now", got.RetryAt, api.AppTaskCapacityRetryDelay)
	}
	if _, err := store.ClaimNextAppTask(context.Background(), "other", before, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim before retry_at = %v; want ErrNotFound", err)
	}
	if claimed, err := store.ClaimNextAppTask(context.Background(), "other", *got.RetryAt, time.Minute); err != nil || claimed.ID != task.ID {
		t.Fatalf("claim at retry_at = %v, %v; want the task again", claimed.ID, err)
	}
}

// The wait is bounded from the first refusal, not from the task's creation:
// once api.AppTaskCapacityMaxWait has passed the release fails as
// capacity_unavailable, a platform code imaged reports as
// release_infrastructure_failed.
func TestAppTaskCoordinatorFailsReleaseAfterCapacityWait(t *testing.T) {
	store, task := newNodeOwnedAppTaskOfKind(t, capacityWaitNode, state.AppTaskKindRelease)
	ctx := context.Background()
	firstRefusal := time.Now().UTC().Add(-api.AppTaskCapacityMaxWait - time.Minute)
	// Record a first refusal that lies beyond the window.
	claimed, err := store.ClaimNextAppTask(ctx, "earlier-schedd", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	started, deferred, err := store.DeferAppTaskForCapacity(ctx, task.ID, *claimed.LeaseToken, firstRefusal,
		firstRefusal.Add(api.AppTaskCapacityRetryDelay), api.AppTaskCapacityMaxWait)
	if err != nil || !deferred || !started.Equal(firstRefusal) {
		t.Fatalf("first refusal = %v, %v, %v; want deferred from %v", started, deferred, err, firstRefusal)
	}
	wantCapacityFailure(t, processCapacityRefusal(t, store, task))
}

// Only release commands wait: a one-off or interactive command fails at once,
// so `gregale app exec` does not hang on a full fleet.
func TestAppTaskCoordinatorFailsManualTaskRefusedForCapacity(t *testing.T) {
	store, task := newNodeOwnedAppTaskOfKind(t, capacityWaitNode, state.AppTaskKindManual)
	wantCapacityFailure(t, processCapacityRefusal(t, store, task))
}

// The store contract on both backends: a deferral needs the current restoring
// lease, keeps the first refusal's time across requeues, ends once the window
// has passed, never applies to other task kinds, and a run clears the wait.
func TestDeferAppTaskForCapacityContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			newStore := func() state.Store {
				if backend == "postgres" {
					return state.NewPgStore(pgtest.OpenMigrated(t))
				}
				return state.NewMemStore()
			}
			store := newStore()
			deferrer := store.(state.AppTaskCapacityDeferrer)
			task := newNodeOwnedAppTaskIn(t, store, "", state.AppTaskKindRelease)
			at := time.Now().UTC().Truncate(time.Microsecond)
			claim := func(at time.Time) string {
				t.Helper()
				claimed, err := store.ClaimNextAppTask(ctx, "schedd", at, time.Minute)
				if err != nil || claimed.ID != task.ID || claimed.LeaseToken == nil {
					t.Fatalf("ClaimNextAppTask(%v) = %v, %v", at, claimed.ID, err)
				}
				return *claimed.LeaseToken
			}
			lease := claim(at)
			if _, deferred, err := deferrer.DeferAppTaskForCapacity(ctx, task.ID, "00000000-0000-4000-8000-000000000000", at,
				at.Add(time.Second), time.Minute); !errors.Is(err, state.ErrAppTaskLeaseLost) || deferred {
				t.Fatalf("stale lease = %v, %v; want ErrAppTaskLeaseLost", deferred, err)
			}
			started, deferred, err := deferrer.DeferAppTaskForCapacity(ctx, task.ID, lease, at, at.Add(time.Second), time.Minute)
			if err != nil || !deferred || !started.Equal(at) {
				t.Fatalf("first refusal = %v, %v, %v; want deferred from %v", started, deferred, err, at)
			}
			if _, err := store.ClaimNextAppTask(ctx, "schedd", at, time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("claim before retry_at = %v; want ErrNotFound", err)
			}
			second := at.Add(30 * time.Second)
			lease = claim(second)
			if started, deferred, err := deferrer.DeferAppTaskForCapacity(ctx, task.ID, lease, second, second.Add(time.Second), time.Minute); err != nil || !deferred || !started.Equal(at) {
				t.Fatalf("second refusal = %v, %v, %v; want deferred, still from %v", started, deferred, err, at)
			}
			late := at.Add(time.Minute)
			lease = claim(late)
			if _, deferred, err := deferrer.DeferAppTaskForCapacity(ctx, task.ID, lease, late, late.Add(time.Second), time.Minute); err != nil || deferred {
				t.Fatalf("refusal after the window = %v, %v; want not deferred", deferred, err)
			}
			if got, err := store.MarkAppTaskRunning(ctx, task.ID, lease, late); err != nil || got.Status != state.AppTaskRunning {
				t.Fatalf("MarkAppTaskRunning = %v, %v", got.Status, err)
			}

			other := newStore()
			manual := newNodeOwnedAppTaskIn(t, other, "", state.AppTaskKindManual)
			at = time.Now().UTC().Truncate(time.Microsecond)
			claimed, err := other.ClaimNextAppTask(ctx, "schedd", at, time.Minute)
			if err != nil || claimed.ID != manual.ID {
				t.Fatalf("claim manual = %v, %v", claimed.ID, err)
			}
			if _, deferred, err := other.(state.AppTaskCapacityDeferrer).DeferAppTaskForCapacity(ctx, manual.ID, *claimed.LeaseToken, at,
				at.Add(time.Second), time.Minute); err != nil || deferred {
				t.Fatalf("manual task = %v, %v; want not deferred", deferred, err)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }

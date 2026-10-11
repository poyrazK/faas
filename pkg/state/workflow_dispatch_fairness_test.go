// adr: 641
package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func createFairDispatchRun(t *testing.T, store Store, app App, tenantID string, due time.Time) *WorkflowRun {
	t.Helper()
	run := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenantID, WorkflowName: "fair",
		DefinitionSnapshot: json.RawMessage(`{"name":"fair","steps":[{"name":"work","run":"work"}]}`), ScheduledFor: due}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return run
}

func claimFairDispatchRun(t *testing.T, store Store, want *WorkflowRun) {
	t.Helper()
	run, err := store.ClaimNextDueWorkflowRun(t.Context())
	if err != nil || run == nil || run.ID != want.ID {
		t.Fatalf("claimed=%+v err=%v, want %s", run, err, want.ID)
	}
}

func createFairDispatchTenant(t *testing.T, store Store, app App) string {
	t.Helper()
	ref := uuid.NewString()
	tenant, _, err := store.(PlatformTenantStore).CreatePlatformTenant(t.Context(), app.AccountID, ref, ref, 10)
	if err != nil {
		t.Fatal(err)
	}
	return tenant.ID
}

func TestWorkflowDispatchFairApplicationsAndHistoryPruning(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		a, _ := seedWorkflowSchedule(t, store, "allow")
		b, _ := seedWorkflowSchedule(t, store, "allow")
		base := time.Now().Add(-time.Hour)
		first := createFairDispatchRun(t, store, a, "", base)
		second := createFairDispatchRun(t, store, a, "", base.Add(time.Second))
		other := createFairDispatchRun(t, store, b, "", base.Add(time.Minute))
		claimFairDispatchRun(t, store, first)
		// Recreating a PgStore must not reset the app's place in service order.
		if pg, ok := store.(*PgStore); ok {
			store = NewPgStore(pg.pool)
		}
		if err := store.MarkWorkflowRunStatus(t.Context(), first.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SweepExpiredWorkflowRuns(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		claimFairDispatchRun(t, store, other)
		claimFairDispatchRun(t, store, second)
	})
}

func TestWorkflowDispatchFairTenantAndUnscopedTurns(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		tenant := createFairDispatchTenant(t, store, app)
		base := time.Now().Add(-time.Hour)
		first := createFairDispatchRun(t, store, app, tenant, base)
		second := createFairDispatchRun(t, store, app, tenant, base.Add(time.Second))
		unscoped := createFairDispatchRun(t, store, app, "", base.Add(time.Minute))
		claimFairDispatchRun(t, store, first)
		if err := store.MarkWorkflowRunStatus(t.Context(), first.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		claimFairDispatchRun(t, store, unscoped)
		claimFairDispatchRun(t, store, second)
	})
}

func TestWorkflowDispatchConcurrentCapacity(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		name := "app"
		limit := api.WorkflowDispatchMaxPerApp
		if scoped {
			name, limit = "tenant", api.WorkflowDispatchMaxPerTenant
		}
		t.Run(name, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				app, _ := seedWorkflowSchedule(t, store, "allow")
				tenantID := ""
				if scoped {
					tenantID = createFairDispatchTenant(t, store, app)
				}
				for range 16 {
					createFairDispatchRun(t, store, app, tenantID, time.Now().Add(-time.Hour))
				}
				start := make(chan struct{})
				results := make(chan *WorkflowRun, 16)
				errorsCh := make(chan error, 16)
				var wg sync.WaitGroup
				for range 16 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						run, err := store.ClaimNextDueWorkflowRun(t.Context())
						if err == nil {
							results <- run
						} else if !errors.Is(err, ErrNotFound) {
							errorsCh <- err
						}
					}()
				}
				close(start)
				wg.Wait()
				close(errorsCh)
				for err := range errorsCh {
					t.Error(err)
				}
				if len(results) != limit {
					t.Fatalf("concurrent claims=%d, want %d", len(results), limit)
				}
				first := <-results
				if _, err := store.CancelWorkflowRun(t.Context(), first.ID, "release dispatch capacity"); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimNextDueWorkflowRun(t.Context()); err != nil {
					t.Fatalf("cancelled claim did not free capacity: %v", err)
				}
			})
		})
	}
}

func TestWorkflowDispatchTenantBacklogLeavesOtherTenantCapacity(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		firstTenant := createFairDispatchTenant(t, store, app)
		secondTenant := createFairDispatchTenant(t, store, app)
		base := time.Now().Add(-time.Hour)
		first := createFairDispatchRun(t, store, app, firstTenant, base)
		createFairDispatchRun(t, store, app, firstTenant, base.Add(time.Second))
		other := createFairDispatchRun(t, store, app, secondTenant, base.Add(time.Minute))
		claimFairDispatchRun(t, store, first)
		claimFairDispatchRun(t, store, other)
		if _, err := store.ClaimNextDueWorkflowRun(t.Context()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("tenant backlog exceeded active dispatch budget: %v", err)
		}
	})
}

func TestWorkflowDispatchRechecksLegacyDefinitionLock(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		pg, ok := store.(*PgStore)
		if !ok {
			return
		}
		app, _ := seedWorkflowSchedule(t, store, "allow")
		var runs []*WorkflowRun
		for index := range 2 {
			run := &WorkflowRun{AppID: app.ID, WorkflowName: "limited", ScheduledFor: time.Now().Add(-time.Hour + time.Duration(index)*time.Minute),
				DefinitionSnapshot: json.RawMessage(`{"name":"limited","max_concurrent_runs":1,"steps":[{"name":"work","run":"work"}]}`)}
			if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			runs = append(runs, run)
		}
		legacy, err := pg.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = legacy.Rollback(t.Context()) }()
		q := sqlc.New()
		if err := q.LockWorkflowRunConcurrency(t.Context(), legacy, sqlc.LockWorkflowRunConcurrencyParams{AppID: mustPgUUID(app.ID), WorkflowName: "limited"}); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := store.ClaimNextDueWorkflowRun(t.Context()); result <- err }()
		// Wait for the new claimant to lock its chosen row before it waits on
		// the previous worker's definition lock. NOWAIT cannot block this test.
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		ticks := time.NewTicker(time.Millisecond)
		defer ticks.Stop()
		locked := false
		for !locked {
			select {
			case <-ticks.C:
				tx, err := pg.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(t.Context(), "SELECT id FROM workflow_runs WHERE id=$1 FOR UPDATE NOWAIT", runs[0].ID)
				_ = tx.Rollback(t.Context())
				var lockErr *pgconn.PgError
				locked = errors.As(err, &lockErr) && lockErr.Code == "55P03"
				if err != nil && !locked {
					t.Fatal(err)
				}
			case <-deadline.C:
				t.Fatal("new claimant did not reach legacy lock")
			}
		}
		if _, err := q.ClaimDueLegacyWorkflowRun(t.Context(), legacy, sqlc.ClaimDueLegacyWorkflowRunParams{ID: mustPgUUID(runs[1].ID), StaleMs: int64(WorkflowRunStaleAfter / time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
		if err := legacy.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("legacy worker's occupied definition slot was bypassed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("claimant remained blocked after legacy commit")
		}
		current, err := store.GetWorkflowRun(t.Context(), runs[0].ID)
		if err != nil || current.Status != WorkflowRunStatusPending {
			t.Fatalf("rejected claim changed run: %+v err=%v", current, err)
		}
	})
}

func TestWorkflowDispatchParkedWaitsReleaseOnlyDispatchCapacity(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		tenant := createFairDispatchTenant(t, store, app)
		first := createFairDispatchRun(t, store, app, tenant, time.Now().Add(-time.Hour))
		second := createFairDispatchRun(t, store, app, tenant, time.Now().Add(-time.Minute))
		claimFairDispatchRun(t, store, first)
		if err := store.ScheduleWorkflowRun(t.Context(), first.ID, WorkflowRunStatusAwaitingEvent, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		claimFairDispatchRun(t, store, second)
		if err := store.MarkWorkflowRunStatus(t.Context(), second.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		// A new definition's lower limit still counts the parked run.
		fourth := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenant, WorkflowName: "fair",
			DefinitionSnapshot: json.RawMessage(`{"name":"fair","max_concurrent_runs":1,"steps":[{"name":"work","run":"work"}]}`)}
		if err := store.CreateWorkflowRun(t.Context(), fourth); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(t.Context()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("parked wait lost definition concurrency occupancy: %v", err)
		}
	})
}

func TestWorkflowDispatchStaleLeaseRecoveryAndCancelledAdmission(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		tenant := createFairDispatchTenant(t, store, app)
		run := createFairDispatchRun(t, store, app, tenant, time.Now().Add(-time.Hour))
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.ClaimNextDueWorkflowRun(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled context admitted work: %v", err)
		}
		claimFairDispatchRun(t, store, run)
		if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*WorkflowStep{{StepName: "work", Status: WorkflowStepStatusRunning, Attempt: 1}}); err != nil {
			t.Fatal(err)
		}
		switch s := store.(type) {
		case *MemStore:
			s.mu.Lock()
			s.workflowRunLeases[run.ID] = time.Now().Add(-time.Minute)
			s.mu.Unlock()
		case *PgStore:
			if _, err := s.pool.Exec(t.Context(), "UPDATE workflow_runs SET lease_until=now()-interval '1 minute' WHERE id=$1", run.ID); err != nil {
				t.Fatal(err)
			}
		}
		claimFairDispatchRun(t, store, run)
		steps, err := store.GetWorkflowSteps(t.Context(), run.ID)
		if err != nil || len(steps) != 1 || steps[0].Status != WorkflowStepStatusPending {
			t.Fatalf("stale steps not recovered: %+v err=%v", steps, err)
		}
	})
}

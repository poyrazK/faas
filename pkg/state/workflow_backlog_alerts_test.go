// adr: 643
package state

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func backlogFixtureTimes(t *testing.T, store Store, run *WorkflowRun, created time.Time, fallback *time.Time) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		fixture := s.workflowRuns[run.ID]
		fixture.CreatedAt = created
		if fallback != nil {
			fixture.UpdatedAt = fallback.Add(-WorkflowRunStaleAfter)
			delete(s.workflowRunLeases, run.ID)
		}
		s.workflowRuns[run.ID] = fixture
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "UPDATE workflow_runs SET created_at=$2 WHERE id=$1", run.ID, created); err != nil {
			t.Fatal(err)
		}
		if fallback != nil {
			if _, err := s.pool.Exec(t.Context(), "UPDATE workflow_runs SET updated_at=$2,lease_until=NULL WHERE id=$1", run.ID, fallback.Add(-WorkflowRunStaleAfter)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWorkflowBacklogSignalsMatchQueueHealth(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, name := range []string{"pending", "parked_due", "expired_lease", "legacy_expired_lease", "live_claim", "future_schedule", "future_wait", "future_retry", "cancelled", "failed", "nominal_fire", "capacity_blocked"} {
			t.Run(name, func(t *testing.T) {
				app, _ := seedWorkflowSchedule(t, store, "allow")
				now := time.Now().UTC().Truncate(time.Microsecond)
				wake := now.Add(-10 * time.Minute)
				if name == "future_schedule" || name == "future_wait" || name == "future_retry" {
					wake = now.Add(time.Hour)
				}
				run := queueHealthRun(t, store, app, "backlog", "", wake, 0)
				backlogFixtureTimes(t, store, run, now.Add(-4*time.Hour), nil)
				wantDue := true
				switch name {
				case "parked_due", "future_wait":
					queueHealthRunning(t, store, run)
					if err := store.ScheduleWorkflowRun(t.Context(), run.ID, WorkflowRunStatusAwaitingEvent, wake); err != nil {
						t.Fatal(err)
					}
					wantDue = name == "parked_due"
				case "expired_lease", "legacy_expired_lease", "live_claim":
					queueHealthRunning(t, store, run)
					if name == "legacy_expired_lease" {
						backlogFixtureTimes(t, store, run, now.Add(-4*time.Hour), &wake)
					} else if name == "expired_lease" {
						queueHealthFixtureLease(t, store, run, wake)
					}
					wantDue = name != "live_claim"
				case "future_retry":
					queueHealthRunning(t, store, run)
					if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*WorkflowStep{{StepName: "work", Status: WorkflowStepStatusPending}}); err != nil {
						t.Fatal(err)
					}
					if _, err := store.StartWorkflowStep(t.Context(), run.ID, "work", 1, json.RawMessage(`{}`)); err != nil {
						t.Fatal(err)
					}
					if err := store.ScheduleWorkflowStepRetry(t.Context(), run.ID, "work", 1, wake, "private retry detail"); err != nil {
						t.Fatal(err)
					}
					wantDue = false
				case "future_schedule":
					wantDue = false
				case "cancelled":
					if _, err := store.CancelWorkflowRun(t.Context(), run.ID, "operator"); err != nil {
						t.Fatal(err)
					}
					wantDue = false
				case "failed":
					if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, WorkflowRunStatusFailed, nil, nil); err != nil {
						t.Fatal(err)
					}
					wantDue = false
				case "nominal_fire":
					backlogFixtureTimes(t, store, run, now, nil)
				case "capacity_blocked":
					for range api.WorkflowDispatchMaxPerApp {
						queueHealthRunning(t, store, queueHealthRun(t, store, app, "other", "", now, 0))
					}
				}
				health, err := store.(WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(t.Context(), app.ID, "backlog", now.Add(-time.Hour), now)
				if err != nil {
					t.Fatal(err)
				}
				at := health.Queue.ObservedAt
				// Age is current state even when no run falls in the count window.
				snapshot, err := store.(WorkflowAlertStore).WorkflowAlertSnapshot(t.Context(), app.AccountID, app.ID, at, at)
				if err != nil || math.Abs(snapshot.DueAgeSeconds-health.Queue.OldestDueAgeSeconds) > 0.01 {
					t.Fatalf("alert=%+v queue=%+v err=%v", snapshot, health.Queue, err)
				}
				if wantDue && name != "nominal_fire" && snapshot.DueAgeSeconds < 600 {
					t.Fatalf("due backlog missed: %+v", snapshot)
				}
				if !wantDue && snapshot.DueAgeSeconds != 0 || name == "nominal_fire" && snapshot.DueAgeSeconds > 5 {
					t.Fatalf("intentional wait or nominal fire inflated backlog: %+v", snapshot)
				}
			})
		}
	})
}

func TestWorkflowBacklogSignalsRespectAppAndAccountScope(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		other, err := store.CreateApp(t.Context(), App{AccountID: app.AccountID, Slug: "second-backlog-app", Type: AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		foreign, _ := seedWorkflowSchedule(t, store, "allow")
		now := time.Now().UTC().Truncate(time.Microsecond)
		for index, fixture := range []App{app, other, foreign} {
			run := queueHealthRun(t, store, fixture, "backlog", "", now.Add(-time.Duration(index+1)*time.Minute), 0)
			backlogFixtureTimes(t, store, run, now.Add(-4*time.Hour), nil)
		}
		for _, tc := range []struct {
			account, app string
			age          float64
		}{{app.AccountID, app.ID, 60}, {app.AccountID, "", 120}, {foreign.AccountID, app.ID, 0}} {
			snapshot, err := store.(WorkflowAlertStore).WorkflowAlertSnapshot(t.Context(), tc.account, tc.app, now, now)
			if err != nil || math.Abs(snapshot.DueAgeSeconds-tc.age) > 0.01 {
				t.Fatalf("scope=%+v snapshot=%+v err=%v", tc, snapshot, err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.(WorkflowAlertStore).WorkflowAlertSnapshot(ctx, app.AccountID, app.ID, now, now); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled read: %v", err)
		}
	})
}

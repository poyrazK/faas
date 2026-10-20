// adr: 643
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func backlogFixtureTimes(t *testing.T, store state.Store, run *state.WorkflowRun, created time.Time, fallback *time.Time) {
	t.Helper()
	switch s := store.(type) {
	case *state.MemStore:
		(*s.Mu()).Lock()
		fixture := (*s.WorkflowRuns())[run.ID]
		fixture.CreatedAt = created
		if fallback != nil {
			fixture.UpdatedAt = fallback.Add(-state.WorkflowRunStaleAfter)
			delete((*s.WorkflowRunLeases()), run.ID)
		}
		(*s.WorkflowRuns())[run.ID] = fixture
		(*s.Mu()).Unlock()
	case *state.PgStore:
		if _, err := (*s.Pool()).Exec(t.Context(), "UPDATE workflow_runs SET created_at=$2 WHERE id=$1", run.ID, created); err != nil {
			t.Fatal(err)
		}
		if fallback != nil {
			if _, err := (*s.Pool()).Exec(t.Context(), "UPDATE workflow_runs SET updated_at=$2,lease_until=NULL WHERE id=$1", run.ID, fallback.Add(-state.WorkflowRunStaleAfter)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWorkflowBacklogSignalsMatchQueueHealth(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		for _, name := range []string{"pending", "parked_due", "expired_lease", "legacy_expired_lease", "live_claim", "future_schedule", "future_wait", "future_retry", "cancelled", "failed", "nominal_fire", "capacity_blocked"} {
			t.Run(name, func(t *testing.T) {
				app, _ := state.SeedWorkflowSchedule(t, store, "allow")
				now := time.Now().UTC().Truncate(time.Microsecond)
				wake := now.Add(-10 * time.Minute)
				if name == "future_schedule" || name == "future_wait" || name == "future_retry" {
					wake = now.Add(time.Hour)
				}
				run := state.QueueHealthRun(t, store, app, "backlog", "", wake, 0)
				backlogFixtureTimes(t, store, run, now.Add(-4*time.Hour), nil)
				wantDue := true
				switch name {
				case "parked_due", "future_wait":
					state.QueueHealthRunning(t, store, run)
					if err := store.ScheduleWorkflowRun(t.Context(), run.ID, state.WorkflowRunStatusAwaitingEvent, wake); err != nil {
						t.Fatal(err)
					}
					wantDue = name == "parked_due"
				case "expired_lease", "legacy_expired_lease", "live_claim":
					state.QueueHealthRunning(t, store, run)
					if name == "legacy_expired_lease" {
						backlogFixtureTimes(t, store, run, now.Add(-4*time.Hour), &wake)
					} else if name == "expired_lease" {
						state.QueueHealthFixtureLease(t, store, run, wake)
					}
					wantDue = name != "live_claim"
				case "future_retry":
					state.QueueHealthRunning(t, store, run)
					if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*state.WorkflowStep{{StepName: "work", Status: state.WorkflowStepStatusPending}}); err != nil {
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
					if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
						t.Fatal(err)
					}
					wantDue = false
				case "nominal_fire":
					backlogFixtureTimes(t, store, run, now, nil)
				case "capacity_blocked":
					for range api.WorkflowDispatchMaxPerApp {
						state.QueueHealthRunning(t, store, state.QueueHealthRun(t, store, app, "other", "", now, 0))
					}
				}
				health, err := store.(state.WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(t.Context(), app.ID, "backlog", now.Add(-time.Hour), now)
				if err != nil {
					t.Fatal(err)
				}
				at := health.Queue.ObservedAt
				// Age is current state even when no run falls in the count window.
				snapshot, err := store.(state.WorkflowAlertStore).WorkflowAlertSnapshot(t.Context(), app.AccountID, app.ID, at, at)
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
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		app, _ := state.SeedWorkflowSchedule(t, store, "allow")
		other, err := store.CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "second-backlog-app", Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		foreign, _ := state.SeedWorkflowSchedule(t, store, "allow")
		now := time.Now().UTC().Truncate(time.Microsecond)
		for index, fixture := range []state.App{app, other, foreign} {
			run := state.QueueHealthRun(t, store, fixture, "backlog", "", now.Add(-time.Duration(index+1)*time.Minute), 0)
			backlogFixtureTimes(t, store, run, now.Add(-4*time.Hour), nil)
		}
		for _, tc := range []struct {
			account, app string
			age          float64
		}{{app.AccountID, app.ID, 60}, {app.AccountID, "", 120}, {foreign.AccountID, app.ID, 0}} {
			snapshot, err := store.(state.WorkflowAlertStore).WorkflowAlertSnapshot(t.Context(), tc.account, tc.app, now, now)
			if err != nil || math.Abs(snapshot.DueAgeSeconds-tc.age) > 0.01 {
				t.Fatalf("scope=%+v snapshot=%+v err=%v", tc, snapshot, err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.(state.WorkflowAlertStore).WorkflowAlertSnapshot(ctx, app.AccountID, app.ID, now, now); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled read: %v", err)
		}
	})
}

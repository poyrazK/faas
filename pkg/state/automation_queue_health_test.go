// adr: 642
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

func queueHealthRun(t *testing.T, store Store, app App, name, tenant string, at time.Time, limit int) *WorkflowRun {
	t.Helper()
	snapshot, err := json.Marshal(api.WorkflowSpec{Name: name, MaxConcurrentRuns: limit, Steps: []api.WorkflowStepSpec{{Name: "work", Run: "work"}}})
	if err != nil {
		t.Fatal(err)
	}
	run := &WorkflowRun{AppID: app.ID, WorkflowName: name, PlatformTenantID: tenant,
		ScheduledFor: at, DefinitionSnapshot: snapshot, Input: json.RawMessage(`{"private":"queue-secret"}`)}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return run
}

func queueHealthRunning(t *testing.T, store Store, run *WorkflowRun) {
	t.Helper()
	if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, WorkflowRunStatusRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func queueHealthFixtureLease(t *testing.T, store Store, run *WorkflowRun, deadline time.Time) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		if s.workflowRunLeases == nil {
			s.workflowRunLeases = make(map[string]time.Time)
		}
		s.workflowRunLeases[run.ID] = deadline
		s.mu.Unlock()
	case *PgStore:
		if _, err := s.pool.Exec(t.Context(), "UPDATE workflow_runs SET lease_until=$2 WHERE id=$1", run.ID, deadline); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutomationQueueHealthReasonsAndDispatchAgreement(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, name := range []string{"ready", "scheduled", "retry_backoff", "parked_wait", "app_capacity", "tenant_capacity", "workflow_capacity", "stale", "due_wait", "due_retry", "rescheduled_retry", "parked_busy"} {
			t.Run(name, func(t *testing.T) {
				app, _ := seedWorkflowSchedule(t, store, "allow")
				now := time.Now().UTC().Truncate(time.Microsecond)
				tenant := ""
				if name == api.AutomationQueueTenantCapacity {
					tenant = createFairDispatchTenant(t, store, app)
				}
				at, limit := now.Add(-time.Minute), 0
				if name == api.AutomationQueueScheduled || name == api.AutomationQueueRetryBackoff || name == api.AutomationQueueParkedWait || name == "rescheduled_retry" || name == "parked_busy" {
					at = now.Add(time.Hour)
				}
				if name == api.AutomationQueueWorkflowCapacity || name == "due_wait" || name == "due_retry" {
					limit = 1
				}
				run := queueHealthRun(t, store, app, "target", tenant, at, limit)
				wantReason, wantWaiting, wantDue := name, int64(1), int64(1)
				switch name {
				case api.AutomationQueueScheduled:
					wantDue = 0
				case api.AutomationQueueRetryBackoff, "due_retry", "rescheduled_retry":
					wantDue = 0
					queueHealthRunning(t, store, run)
					if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*WorkflowStep{{StepName: "work", Status: WorkflowStepStatusPending}}); err != nil {
						t.Fatal(err)
					}
					if _, err := store.StartWorkflowStep(t.Context(), run.ID, "work", 1, json.RawMessage(`{}`)); err != nil {
						t.Fatal(err)
					}
					if err := store.ScheduleWorkflowStepRetry(t.Context(), run.ID, "work", 1, at, "private retry failure"); err != nil {
						t.Fatal(err)
					}
					if name == "due_retry" {
						wantReason, wantDue = api.AutomationQueueReady, 1
					}
					if name == "rescheduled_retry" {
						// An earlier action-capacity poll is not the retry deadline.
						if err := store.SetWorkflowRunWake(t.Context(), run.ID, WorkflowRunStatusPending, now.Add(30*time.Minute)); err != nil {
							t.Fatal(err)
						}
						wantReason = api.AutomationQueueScheduled
					}
				case api.AutomationQueueParkedWait, "due_wait", "parked_busy":
					queueHealthRunning(t, store, run)
					if err := store.ScheduleWorkflowRun(t.Context(), run.ID, WorkflowRunStatusAwaitingEvent, at); err != nil {
						t.Fatal(err)
					}
					if name == "due_wait" {
						wantReason = api.AutomationQueueReady
					} else {
						wantReason, wantDue = api.AutomationQueueParkedWait, 0
					}
					if name == "parked_busy" {
						for range api.WorkflowDispatchMaxPerApp {
							queueHealthRunning(t, store, queueHealthRun(t, store, app, "other", "", now, 0))
						}
					}
				case api.AutomationQueueAppCapacity:
					for range api.WorkflowDispatchMaxPerApp {
						queueHealthRunning(t, store, queueHealthRun(t, store, app, "other", "", now, 0))
					}
				case api.AutomationQueueTenantCapacity:
					queueHealthRunning(t, store, queueHealthRun(t, store, app, "other", tenant, now, 0))
				case api.AutomationQueueWorkflowCapacity:
					parked := queueHealthRun(t, store, app, "target", "", now.Add(time.Hour), 1)
					queueHealthRunning(t, store, parked)
					if err := store.ScheduleWorkflowRun(t.Context(), parked.ID, WorkflowRunStatusAwaitingEvent, now.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					wantWaiting = 2
				case "stale":
					queueHealthRunning(t, store, run)
					queueHealthFixtureLease(t, store, run, now.Add(-time.Minute))
					wantReason = api.AutomationQueueReady
				}
				// Historical selection must not hide the current queue.
				health, err := store.(WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(t.Context(), app.ID, "target", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				q := health.Queue
				if health.RunCount != 0 || q == nil || q.WaitingRunCount != wantWaiting || q.DueRunCount != wantDue || q.ReasonCounts[wantReason] != 1 || len(q.ReasonCounts) != 7 {
					t.Fatalf("queue diagnostics=%+v historical=%d", q, health.RunCount)
				}
				var total int64
				for _, count := range q.ReasonCounts {
					total += count
				}
				if total != q.WaitingRunCount || q.ObservedAt.Before(now) || q.AppAtCapacity != (name == api.AutomationQueueAppCapacity || name == "parked_busy") {
					t.Fatalf("inconsistent snapshot: %+v", q)
				}
				if name == "stale" && (q.StaleRunCount != 1 || q.AppRunningCount != 0) {
					t.Fatalf("stale lease consumed capacity: %+v", q)
				}
				if wantDue == 0 && q.OldestDueAgeSeconds != 0 {
					t.Fatalf("intentional wait aged as due: %+v", q)
				}
				// Test admission agreement directly, leaving other workflows out
				// of the claim queue and finishing each fixture before the next.
				if wantReason == api.AutomationQueueReady {
					claimed, err := store.ClaimNextDueWorkflowRun(t.Context())
					if err != nil || claimed == nil || claimed.ID != run.ID {
						t.Fatalf("ready diagnostics disagree with claim: %+v %v", claimed, err)
					}
				} else if claimed, err := store.ClaimNextDueWorkflowRun(t.Context()); !errors.Is(err, ErrNotFound) || claimed != nil {
					t.Fatalf("waiting diagnostics disagree with claim: %+v %v", claimed, err)
				}
				runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, ListWorkflowRunsOpts{Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				for _, existing := range runs {
					if _, err := store.CancelWorkflowRun(t.Context(), existing.ID, "fixture finished"); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	})
}

func TestAutomationQueueHealthScopeAgeAndCancellation(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		foreign, _ := seedWorkflowSchedule(t, store, "allow")
		now := time.Now().UTC().Truncate(time.Microsecond)
		for range api.WorkflowDispatchMaxPerApp {
			queueHealthRunning(t, store, queueHealthRun(t, store, foreign, "target", "", now, 0))
		}
		run := queueHealthRun(t, store, app, "target", "", now.Add(-time.Hour), 0)
		health, err := store.(WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(t.Context(), app.ID, "target", now.Add(-time.Hour), now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if health.Queue.AppRunningCount != 0 || health.Queue.ReasonCounts[api.AutomationQueueReady] != 1 || health.Queue.OldestDueAgeSeconds > 5 {
			t.Fatalf("foreign scope or nominal fire leaked into age: %+v", health.Queue)
		}
		if _, err := store.CancelWorkflowRun(t.Context(), run.ID, "cancelled"); err != nil {
			t.Fatal(err)
		}
		health, err = store.(WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(t.Context(), app.ID, "target", now.Add(-time.Hour), now.Add(time.Hour))
		if err != nil || health.Queue.WaitingRunCount != 0 || health.Queue.OldestDueAgeSeconds != 0 {
			t.Fatalf("cancelled run remains waiting: %+v %v", health.Queue, err)
		}
		// Two hours spent waiting must not count as two hours of due backlog.
		wake := now.Add(-time.Minute)
		aged := queueHealthRun(t, store, app, "target", "", wake, 0)
		created := now.Add(-2 * time.Hour)
		switch s := store.(type) {
		case *MemStore:
			s.mu.Lock()
			fixture := s.workflowRuns[aged.ID]
			fixture.CreatedAt = created
			s.workflowRuns[aged.ID] = fixture
			s.mu.Unlock()
		case *PgStore:
			if _, err := s.pool.Exec(t.Context(), "UPDATE workflow_runs SET created_at=$2 WHERE id=$1", aged.ID, created); err != nil {
				t.Fatal(err)
			}
		}
		health, err = store.(WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(t.Context(), app.ID, "target", now.Add(-time.Hour), now)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(health.Queue.OldestDueAgeSeconds-health.Queue.ObservedAt.Sub(wake).Seconds()) > 0.01 {
			t.Fatalf("queue age included intentional wait: %+v", health.Queue)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.(WorkflowAutomationHealthStore).GetWorkflowAutomationHealth(ctx, app.ID, "target", now.Add(-time.Hour), now); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled read: %v", err)
		}
	})
}

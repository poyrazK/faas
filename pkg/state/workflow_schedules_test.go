package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func seedWorkflowSchedule(t *testing.T, store Store, overlap string) (App, Deployment) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "schedule-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := json.Marshal([]api.WorkflowSpec{{Name: "nightly", Trigger: &api.WorkflowTriggerSpec{
		Type: "schedule", Schedule: "* * * * *", Overlap: overlap, Input: json.RawMessage(`{"report":"daily"}`)},
		Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/report"}}}})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: DeployPending, Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	return app, deployment
}

func workflowScheduleStores(t *testing.T, test func(*testing.T, Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(context.Background(), pool); err != nil {
			t.Fatal(err)
		}
		test(t, NewPgStore(pool))
	})
}

func TestWorkflowSchedules(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, test := range []struct {
			name string
			run  func(*testing.T, Store)
		}{
			{"concurrent admission", testWorkflowScheduleConcurrentAdmission},
			{"overlap and downtime", testWorkflowScheduleOverlapAndMissedMinutes},
			{"manual quota contention", testWorkflowScheduleSharesManualQuota},
			{"admission guards", testWorkflowScheduleAdmissionGuards},
			{"deployment changes", testWorkflowScheduleDeploymentChanges},
			{"run retention", testWorkflowScheduleDedupSurvivesRunRetention},
			{"transaction rollback", testWorkflowSchedulePostgresAdmissionRollback},
		} {
			t.Run(test.name, func(t *testing.T) { test.run(t, store) })
		}
	})
}

func testWorkflowScheduleConcurrentAdmission(t *testing.T, store Store) {
	app, deployment := seedWorkflowSchedule(t, store, "allow")
	schedules := store.(WorkflowScheduleStore)
	ctx := context.Background()
	at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	if cursor, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil || !changed || cursor.Status != WorkflowScheduleArmed {
		t.Fatalf("arm = %+v, changed=%t, err=%v", cursor, changed, err)
	}
	var started atomic.Int32
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			cursor, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute))
			if err != nil {
				t.Error(err)
			}
			if changed && cursor.Status == WorkflowScheduleStarted {
				started.Add(1)
			}
		}()
	}
	group.Wait()
	runs, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 100})
	if err != nil || started.Load() != 1 || total != 1 || len(runs) != 1 {
		t.Fatalf("started=%d total=%d runs=%d err=%v", started.Load(), total, len(runs), err)
	}
	if !equalWorkflowJSON(runs[0].Input, json.RawMessage(`{"report":"daily"}`)) || !runs[0].ScheduledFor.Equal(at.Add(time.Minute).Truncate(time.Minute)) {
		t.Fatalf("run input/time = %s/%s", runs[0].Input, runs[0].ScheduledFor)
	}
}

func testWorkflowScheduleOverlapAndMissedMinutes(t *testing.T, store Store) {
	app, deployment := seedWorkflowSchedule(t, store, "skip")
	schedules, ctx := store.(WorkflowScheduleStore), context.Background()
	at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
		t.Fatal(err)
	}
	cursor, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute))
	if err != nil || cursor.Status != WorkflowScheduleStarted {
		t.Fatalf("start=%+v err=%v", cursor, err)
	}
	firstID := cursor.LastRunID
	cursor, _, err = schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(2*time.Minute))
	if err != nil || cursor.Status != WorkflowScheduleSkippedOverlap {
		t.Fatalf("overlap=%+v err=%v", cursor, err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, firstID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Restart after one hour: admit only the current minute, never replay
	// the skipped overlap or the 58 intervening missed occurrences.
	cursor, _, err = schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Hour))
	if err != nil || cursor.Status != WorkflowScheduleStarted {
		t.Fatalf("restart=%+v err=%v", cursor, err)
	}
	_, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 100})
	if err != nil || total != 2 {
		t.Fatalf("total=%d err=%v", total, err)
	}
}

func testWorkflowScheduleSharesManualQuota(t *testing.T, store Store) {
	app, deployment := seedWorkflowSchedule(t, store, "allow")
	schedules, ctx := store.(WorkflowScheduleStore), context.Background()
	at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
		t.Fatal(err)
	}
	maxActive := api.PlanHobby.WorkflowMaxConcurrentRuns()
	for i := 0; i < maxActive-1; i++ {
		if _, err := store.CreateWorkflowRunAdmitted(ctx, &WorkflowRun{AppID: app.ID, WorkflowName: "manual", DefinitionSnapshot: json.RawMessage(`{}`)}, maxActive); err != nil {
			t.Fatal(err)
		}
	}
	var group sync.WaitGroup
	for i := range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			if i%2 == 0 {
				_, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute))
				if err != nil {
					t.Error(err)
				}
			} else {
				_, err := store.CreateWorkflowRunAdmitted(ctx, &WorkflowRun{AppID: app.ID, WorkflowName: fmt.Sprint(i), DefinitionSnapshot: json.RawMessage(`{}`)}, maxActive)
				if err != nil && !errors.Is(err, ErrWorkflowRunQuotaExceeded) {
					t.Error(err)
				}
			}
		}()
	}
	group.Wait()
	active, err := store.CountActiveRunsByApp(ctx, app.ID)
	if err != nil || active != maxActive {
		t.Fatalf("active=%d limit=%d err=%v", active, maxActive, err)
	}
}

func testWorkflowScheduleAdmissionGuards(t *testing.T, store Store) {
	for _, test := range []struct {
		name  string
		block func(context.Context, App) error
	}{
		{"maintenance", func(ctx context.Context, app App) error {
			value := true
			_, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{MaintenanceMode: &value, SetMaintenanceMode: true})
			return err
		}},
		{"tenant required", func(ctx context.Context, app App) error {
			value := true
			_, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{PlatformTenantRequired: &value, SetPlatformTenantRequired: true})
			return err
		}},
		{"suspended account", func(ctx context.Context, app App) error {
			return store.UpdateAccountStatus(ctx, app.AccountID, AccountSuspended)
		}},
		{"free plan", func(ctx context.Context, app App) error {
			return store.UpdateAccountPlan(ctx, app.AccountID, api.PlanFree)
		}},
		{"abuse hold", func(ctx context.Context, app App) error {
			_, err := store.(AccountAbuseHoldStore).SetAccountAbuseHold(ctx, app.AccountID, AccountAbuseHoldOperator, time.Now())
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, deployment := seedWorkflowSchedule(t, store, "allow")
			schedules, ctx := store.(WorkflowScheduleStore), context.Background()
			at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
			if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
				t.Fatal(err)
			}
			if err := test.block(ctx, app); err != nil {
				t.Fatal(err)
			}
			if _, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute)); err != nil || changed {
				t.Fatalf("blocked admission: changed=%t err=%v", changed, err)
			}
			candidates, err := schedules.ListWorkflowScheduleCandidates(ctx, "", "", 256)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range candidates {
				if candidate.AppID == app.ID {
					t.Fatal("blocked app remains a schedule candidate")
				}
			}
		})
	}
}

func testWorkflowScheduleDeploymentChanges(t *testing.T, store Store) {
	app, deployment := seedWorkflowSchedule(t, store, "allow")
	schedules, ctx := store.(WorkflowScheduleStore), context.Background()
	at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
		t.Fatal(err)
	}
	preview, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "preview", Kind: DeploymentKindImage,
		ImageDigest: "sha256:preview", Status: DeployLive, Workflows: deployment.Workflows})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, preview.ID, "nightly", at.Add(time.Minute)); err != nil || changed {
		t.Fatalf("preview admission: changed=%t err=%v", changed, err)
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(deployment.Workflows, &definitions); err != nil {
		t.Fatal(err)
	}
	disabled := false
	definitions[0].Trigger.Enabled = &disabled
	raw, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:disabled", Status: DeployPending, Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{deployment.ID, replacement.ID} {
		if _, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, id, "nightly", at.Add(2*time.Minute)); err != nil || changed {
			t.Fatalf("stale/disabled admission: changed=%t err=%v", changed, err)
		}
	}
	// Re-enabling is a fresh deployment: arm, then fire on the next minute.
	replacement, err = store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:enabled", Status: DeployPending, Workflows: deployment.Workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	cursor, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, replacement.ID, "nightly", at.Add(3*time.Minute))
	if err != nil || !changed || cursor.Status != WorkflowScheduleArmed {
		t.Fatalf("rearm=%+v err=%v", cursor, err)
	}
	cursor, changed, err = schedules.AdmitScheduledWorkflow(ctx, app.ID, replacement.ID, "nightly", at.Add(4*time.Minute))
	if err != nil || !changed || cursor.Status != WorkflowScheduleStarted {
		t.Fatalf("fire=%+v err=%v", cursor, err)
	}
}

func testWorkflowScheduleDedupSurvivesRunRetention(t *testing.T, store Store) {
	app, deployment := seedWorkflowSchedule(t, store, "allow")
	schedules, ctx := store.(WorkflowScheduleStore), context.Background()
	at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
		t.Fatal(err)
	}
	cursor, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, cursor.LastRunID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Expiration compares finish time with the current clock. Let the timestamp
	// advance so this test doesn't depend on sub-microsecond clock resolution.
	time.Sleep(time.Millisecond)
	if deleted, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || deleted < 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, changed, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute)); err != nil || changed {
		t.Fatalf("retained dedup: changed=%t err=%v", changed, err)
	}
	cursors, err := schedules.ListWorkflowScheduleCursors(ctx, app.ID)
	if err != nil || len(cursors) != 1 || cursors[0].LastRunID != "" || cursors[0].Status != WorkflowScheduleStarted {
		t.Fatalf("retained cursor=%+v err=%v", cursors, err)
	}
}

func testWorkflowSchedulePostgresAdmissionRollback(t *testing.T, backend Store) {
	store, ok := backend.(*PgStore)
	if !ok {
		return
	}
	pool, ctx := store.pool, context.Background()
	app, deployment := seedWorkflowSchedule(t, store, "allow")
	at := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	if _, _, err := store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
		t.Fatal(err)
	}
	// Force failure after the run insert, proving cursor and run commit together.
	if _, err := pool.Exec(ctx, "ALTER TABLE workflow_schedule_cursors ADD CONSTRAINT reject_start CHECK (status <> 'started') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute)); err == nil {
		t.Fatal("expected cursor write failure")
	}
	if _, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
		t.Fatalf("rollback runs=%d err=%v", total, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE workflow_schedule_cursors DROP CONSTRAINT reject_start"); err != nil {
		t.Fatal(err)
	}
	cursor, changed, err := store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute))
	if err != nil || !changed || cursor.Status != WorkflowScheduleStarted {
		t.Fatalf("retry=%+v err=%v", cursor, err)
	}
}

func TestWorkflowScheduleCalendarAndClockRollback(t *testing.T) {
	for _, test := range []struct {
		name, timezone, schedule, previous, now string
		starts                                  bool
	}{
		{"spring forward", "Europe/Berlin", "30 2 * * *", "2026-03-28T01:30:00Z", "2026-03-29T01:00:30Z", true},
		{"fall back second wall minute", "Europe/Berlin", "30 2 * * *", "2026-10-25T00:30:30Z", "2026-10-25T01:30:30Z", false},
		{"clock rollback", "UTC", "* * * * *", "2026-10-02T10:02:30Z", "2026-10-02T10:01:30Z", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous, err := time.Parse(time.RFC3339, test.previous)
			if err != nil {
				t.Fatal(err)
			}
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			spec := api.WorkflowSpec{Name: "report", Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: test.schedule, Timezone: test.timezone}, Steps: []api.WorkflowStepSpec{{Name: "main", Run: "report"}}}
			trigger, err := json.Marshal(spec.Trigger)
			if err != nil {
				t.Fatal(err)
			}
			_, run, err := evaluateWorkflowSchedule("app", "deployment", spec,
				&WorkflowScheduleCursor{DeploymentID: "deployment", TriggerSnapshot: trigger, LastEvaluatedAt: previous}, now, 0, 0, 10)
			if err != nil || (run != nil) != test.starts {
				t.Fatalf("run=%+v err=%v", run, err)
			}
		})
	}
}

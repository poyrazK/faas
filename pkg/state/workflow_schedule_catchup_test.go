// adr: 639
package state

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowScheduleCatchUpCalendar(t *testing.T) {
	for _, tc := range []struct {
		name, policy, window, cron, zone, previous, now, want string
	}{
		{"default skip", "", "", "*/5 * * * *", "UTC", "2026-10-07T10:00:30Z", "2026-10-07T10:17:30Z", ""},
		{"latest coalesces", "latest", "", "*/5 * * * *", "UTC", "2026-10-07T10:00:30Z", "2026-10-07T10:17:30Z", "2026-10-07T10:15:00Z"},
		{"current minute wins", "latest", "", "*/5 * * * *", "UTC", "2026-10-07T10:00:30Z", "2026-10-07T10:20:30Z", "2026-10-07T10:20:00Z"},
		{"expired", "latest", "1h", "0 7 * * *", "UTC", "2026-10-07T06:59:30Z", "2026-10-07T08:00:01Z", ""},
		{"inclusive window", "latest", "1h", "0 7 * * *", "UTC", "2026-10-07T06:59:30Z", "2026-10-07T08:00:00Z", "2026-10-07T07:00:00Z"},
		{"consumed interval", "latest", "", "0 7 * * *", "UTC", "2026-10-07T07:20:30Z", "2026-10-07T07:30:30Z", ""},
		{"bounded long outage", "latest", "24h", "0 7 * * *", "UTC", "2025-10-07T06:59:30Z", "2026-10-07T07:20:30Z", "2026-10-07T07:00:00Z"},
		{"spring gap recovery", "latest", "1h", "30 2 * * *", "Europe/Berlin", "2026-03-28T01:30:30Z", "2026-03-29T01:20:30Z", "2026-03-29T01:00:00Z"},
		{"fall back recovery", "latest", "2h", "30 2 * * *", "Europe/Berlin", "2026-10-25T00:20:30Z", "2026-10-25T01:45:30Z", "2026-10-25T00:30:00Z"},
		{"fall back consumed", "latest", "2h", "30 2 * * *", "Europe/Berlin", "2026-10-25T00:30:30Z", "2026-10-25T01:45:30Z", ""},
		{"clock rollback", "latest", "", "*/5 * * * *", "UTC", "2026-10-07T10:20:30Z", "2026-10-07T10:17:30Z", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := func(raw string) time.Time {
				v, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			spec := api.WorkflowSpec{Name: "report", Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: tc.cron, Timezone: tc.zone, CatchUp: tc.policy, CatchUpWindow: tc.window}, Steps: []api.WorkflowStepSpec{{Name: "main", Run: "report"}}}
			trigger, err := json.Marshal(spec.Trigger)
			if err != nil {
				t.Fatal(err)
			}
			previous := &WorkflowScheduleCursor{DeploymentID: "00000000-0000-0000-0000-000000000640", TriggerSnapshot: trigger, LastEvaluatedAt: parse(tc.previous)}
			cursor, run, err := evaluateWorkflowSchedule("app", "", "00000000-0000-0000-0000-000000000640", spec, previous, parse(tc.now), 0, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if run != nil {
					t.Fatalf("unexpected recovered run: %+v", run)
				}
			} else if run == nil || !run.ScheduledFor.Equal(parse(tc.want)) || !cursor.LastEvaluatedAt.Equal(parse(tc.now)) {
				t.Fatalf("cursor=%+v run=%+v want=%s", cursor, run, tc.want)
			}
		})
	}
}

func seedCatchUpSchedule(t *testing.T, store Store, tenantRequired bool, overlap string) (App, Deployment, string) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "recovery-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256, PlatformTenantRequired: tenantRequired})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := json.Marshal([]api.WorkflowSpec{{Name: "report", Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "*/5 * * * *", CatchUp: "latest", CatchUpWindow: "2h", Overlap: overlap, TenantConfigurable: tenantRequired, Input: json.RawMessage(`{"report":"daily"}`)}, Steps: []api.WorkflowStepSpec{{Name: "main", Run: "report"}}}})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:recovery", Status: DeployPending, Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	tenantID := ""
	if tenantRequired {
		tenants := store.(PlatformTenantStore)
		ref := uuid.NewString()
		tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, ref, ref, 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, ref, ref)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenants.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		tenantID = tenant.ID
	}
	return app, deployment, tenantID
}

func catchUpAdmitter(store Store, app App, deployment Deployment, tenantID string) func(time.Time) (WorkflowScheduleCursor, bool, error) {
	return func(at time.Time) (WorkflowScheduleCursor, bool, error) {
		if tenantID != "" {
			return store.(TenantWorkflowScheduleStore).AdmitTenantScheduledWorkflow(context.Background(), app.ID, tenantID, deployment.ID, "report", at)
		}
		return store.(WorkflowScheduleStore).AdmitScheduledWorkflow(context.Background(), app.ID, deployment.ID, "report", at)
	}
}

func TestWorkflowScheduleCatchUpConcurrentRecoveryAndHistory(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, tenant := range []bool{false, true} {
			app, deployment, tenantID := seedCatchUpSchedule(t, store, tenant, "allow")
			admit := catchUpAdmitter(store, app, deployment, tenantID)
			at := time.Date(2026, 10, 7, 10, 0, 30, 0, time.UTC)
			if cursor, changed, err := admit(at); err != nil || !changed || cursor.Status != WorkflowScheduleArmed {
				t.Fatalf("arm=%+v changed=%t err=%v", cursor, changed, err)
			}
			now := at.Add(17 * time.Minute)
			var started atomic.Int32
			var wg sync.WaitGroup
			for range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cursor, changed, err := admit(now)
					if err != nil {
						t.Error(err)
					}
					if changed && cursor.Status == WorkflowScheduleStarted {
						started.Add(1)
					}
				}()
			}
			wg.Wait()
			if started.Load() != 1 {
				t.Fatalf("recovered admissions=%d tenant=%t", started.Load(), tenant)
			}
			runs, total, err := store.ListWorkflowRuns(context.Background(), app.ID, ListWorkflowRunsOpts{Limit: 10})
			want := at.Truncate(time.Minute).Add(15 * time.Minute)
			if err != nil || total != 1 || len(runs) != 1 || !runs[0].ScheduledFor.Equal(want) || runs[0].PlatformTenantID != tenantID {
				t.Fatalf("runs=%+v total=%d err=%v", runs, total, err)
			}
			if cursor, changed, err := admit(now.Add(time.Minute)); err != nil || changed || !cursor.LastEvaluatedAt.Equal(now.Add(time.Minute)) || cursor.LastAdmittedAt == nil || !cursor.LastAdmittedAt.Equal(now.Truncate(time.Minute)) {
				t.Fatalf("non-due cursor=%+v changed=%t err=%v", cursor, changed, err)
			}
			history, err := store.(WorkflowScheduleHistoryStore).ListWorkflowScheduleOccurrences(context.Background(), app.ID, tenantID, "", 100)
			if err != nil || len(history) != 1 || !history[0].ScheduledFor.Equal(want) || !history[0].EvaluatedAt.Equal(now) || history[0].RunID != runs[0].ID {
				t.Fatalf("history=%+v err=%v", history, err)
			}
			if cursor, _, err := admit(now.Add(30 * time.Minute)); err != nil || cursor.Status != WorkflowScheduleStarted || cursor.ScheduledFor == nil || !cursor.ScheduledFor.Equal(want.Add(30*time.Minute)) {
				t.Fatalf("next recovery=%+v err=%v", cursor, err)
			}
		}
	})
}

func TestWorkflowScheduleCatchUpSkipsConsumeInterval(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for _, tenant := range []bool{false, true} {
			for _, quota := range []bool{false, true} {
				app, deployment, tenantID := seedCatchUpSchedule(t, store, tenant, "skip")
				admit := catchUpAdmitter(store, app, deployment, tenantID)
				at := time.Date(2026, 10, 7, 10, 0, 30, 0, time.UTC)
				if _, _, err := admit(at); err != nil {
					t.Fatal(err)
				}
				count, name, wantStatus := 1, "report", WorkflowScheduleSkippedOverlap
				if quota {
					count, name, wantStatus = api.PlanHobby.WorkflowMaxConcurrentRuns(), "other", WorkflowScheduleSkippedQuota
				}
				var runIDs []string
				for range count {
					raw, err := json.Marshal(api.WorkflowSpec{Name: name, Steps: []api.WorkflowStepSpec{{Name: "main", Run: "report"}}})
					if err != nil {
						t.Fatal(err)
					}
					run := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenantID, WorkflowName: name, DefinitionSnapshot: raw}
					if err := store.CreateWorkflowRun(context.Background(), run); err != nil {
						t.Fatal(err)
					}
					runIDs = append(runIDs, run.ID)
				}
				if cursor, changed, err := admit(at.Add(17 * time.Minute)); err != nil || !changed || cursor.Status != wantStatus {
					t.Fatalf("skip=%+v changed=%t err=%v", cursor, changed, err)
				}
				for _, id := range runIDs {
					if err := store.MarkWorkflowRunStatus(context.Background(), id, WorkflowRunStatusSucceeded, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if cursor, changed, err := admit(at.Add(18 * time.Minute)); err != nil || changed || cursor.Status != wantStatus {
					t.Fatalf("replayed skip=%+v changed=%t err=%v", cursor, changed, err)
				}
				history, err := store.(WorkflowScheduleHistoryStore).ListWorkflowScheduleOccurrences(context.Background(), app.ID, tenantID, "", 100)
				if err != nil || len(history) != 1 || history[0].Status != wantStatus {
					t.Fatalf("history=%+v err=%v", history, err)
				}
			}
		}
	})
}

func TestTenantWorkflowCatchUpPreservesCadenceAndRearmsOnPublication(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, deployment, tenantID := seedCatchUpSchedule(t, store, true, "allow")
		schedules := store.(TenantWorkflowScheduleStore)
		config, err := schedules.UpdateTenantWorkflowSchedule(context.Background(), app.AccountID, tenantID, app.ID, "report", 0, "*/10 * * * *", "UTC", "allow", true)
		if err != nil || config.CatchUp != "latest" || config.CatchUpWindow != "2h0m0s" || config.Version != 1 {
			t.Fatalf("config=%+v err=%v", config, err)
		}
		admit := catchUpAdmitter(store, app, deployment, tenantID)
		now := time.Now().UTC().Truncate(time.Hour).Add(3*time.Hour + 27*time.Minute)
		cursor, changed, err := admit(now)
		if err != nil || !changed || cursor.Status != WorkflowScheduleStarted || cursor.ScheduledFor == nil || !cursor.ScheduledFor.Equal(now.Add(-7*time.Minute)) {
			t.Fatalf("customized recovery=%+v changed=%t err=%v", cursor, changed, err)
		}
		author := store.(AutomationStore)
		draft := mutateForTest(t, author, app.ID, "report", "save", 0, automationDraft("report", "/report", `{"type":"schedule","schedule":"*/5 * * * *","tenant_configurable":true,"catch_up":"latest","catch_up_window":"1h"}`), false)
		published := mutateForTest(t, author, app.ID, "report", "publish", draft.Version, nil, true)
		if cursor, changed, err := admit(now.Add(time.Hour)); err != nil || !changed || cursor.Status != WorkflowScheduleArmed || cursor.ScheduledFor != nil || cursor.LastAdmittedAt == nil || !cursor.LastAdmittedAt.Equal(now) {
			t.Fatalf("publication rearm=%+v changed=%t err=%v", cursor, changed, err)
		}
		configs, err := schedules.ListTenantWorkflowSchedules(context.Background(), app.AccountID, tenantID, app.ID)
		if err != nil || len(configs) != 1 || configs[0].Schedule != "*/10 * * * *" || configs[0].Version != 1 || configs[0].CatchUpWindow != "1h0m0s" {
			t.Fatalf("preserved config=%+v err=%v", configs, err)
		}
		paused, err := author.MutateAutomation(context.Background(), app.ID, "report", AutomationMutation{Action: "enable", ExpectedVersion: published.Version, Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateAutomation(context.Background(), app.ID, "report", AutomationMutation{Action: "enable", ExpectedVersion: paused.Version, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if cursor, changed, err := admit(now.Add(2 * time.Hour)); err != nil || !changed || cursor.Status != WorkflowScheduleArmed {
			t.Fatalf("resume replayed pause=%+v changed=%t err=%v", cursor, changed, err)
		}
	})
}

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTenantWorkflowScheduleFairnessSurvivesPagingAndRunRetention(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "fair-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256, PlatformTenantRequired: true})
		if err != nil {
			t.Fatal(err)
		}
		definitions := []api.WorkflowSpec{}
		for _, name := range []string{"daily", "report"} {
			definitions = append(definitions, api.WorkflowSpec{Name: name, Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Overlap: "allow"}, Steps: []api.WorkflowStepSpec{{Name: "main", Run: "report"}}})
		}
		raw, err := json.Marshal(definitions)
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:fair", Status: DeployPending, Workflows: raw})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		tenants := store.(PlatformTenantStore)
		var firstTenant string
		for i := range 12 {
			ref := fmt.Sprintf("fair-%d-%s", i, uuid.NewString())
			tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, ref, ref, 20)
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
			firstTenant = tenant.ID
		}
		fair := store.(FairTenantWorkflowScheduleStore)
		schedules := store.(TenantWorkflowScheduleStore)
		history := store.(WorkflowScheduleHistoryStore)
		at := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
		admitted := map[string]int{}
		for cycle := range 4 {
			now := at.Add(time.Duration(cycle)*time.Minute + 30*time.Second)
			var runs []string
			evaluated := map[string]bool{}
			for {
				// Small pages force priority changes between database reads.
				page, err := fair.ListFairTenantWorkflowScheduleCandidates(ctx, "", now.Truncate(time.Minute), 3)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				for _, candidate := range page {
					var specs []api.WorkflowSpec
					if err := json.Unmarshal(candidate.Workflows, &specs); err != nil || len(specs) != 1 {
						t.Fatalf("fair candidate must contain one workflow: %s %v", candidate.Workflows, err)
					}
					key := candidate.PlatformTenantID + "/" + specs[0].Name
					if evaluated[key] {
						t.Fatalf("pair revisited within sweep: %s", key)
					}
					evaluated[key] = true
					cursor, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, app.ID, candidate.PlatformTenantID, deployment.ID, specs[0].Name, now)
					if err != nil || !changed {
						t.Fatalf("admission=%+v changed=%t err=%v", cursor, changed, err)
					}
					if cursor.Status == WorkflowScheduleStarted {
						admitted[key]++
						runs = append(runs, cursor.LastRunID)
					}
				}
			}
			if len(evaluated) != 24 {
				t.Fatalf("evaluated %d of 24 pairs", len(evaluated))
			}
			if cycle == 0 {
				if len(runs) != 0 {
					t.Fatal("new schedules must arm")
				}
				continue
			}
			if len(runs) != account.Plan.WorkflowMaxConcurrentRuns() {
				t.Fatalf("admitted=%d quota=%d", len(runs), account.Plan.WorkflowMaxConcurrentRuns())
			}
			snapshot, err := store.(WorkflowAlertStore).WorkflowAlertSnapshot(ctx, account.ID, app.ID, at.Add(-time.Hour), time.Now().UTC().Add(2*time.Minute))
			if err != nil || snapshot.QuotaSkips != int64(cycle*14) || snapshot.PendingAgeSeconds < 119 {
				t.Fatalf("signals=%+v err=%v", snapshot, err)
			}
			for _, runID := range runs {
				if err := store.MarkWorkflowRunStatus(ctx, runID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			// Fair priority must outlive the workflow rows and their foreign keys.
			if _, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil {
				t.Fatal(err)
			}
		}
		if len(admitted) != 24 {
			t.Fatalf("starvation: only %d of 24 pairs admitted", len(admitted))
		}
		// All 72 due outcomes must survive retention, with stable keyset paging.
		seen := map[string]bool{}
		before := ""
		started := 0
		for {
			rows, err := history.ListWorkflowScheduleOccurrences(ctx, app.ID, "", before, 7)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				if seen[row.ID] {
					t.Fatal("duplicate occurrence across pages")
				}
				seen[row.ID] = true
				if row.Status == WorkflowScheduleStarted {
					started++
					if row.RunID == "" {
						t.Fatal("retention removed historical run identity")
					}
				}
			}
			before = rows[len(rows)-1].ID
		}
		if len(seen) != 72 || started != 30 {
			t.Fatalf("history: total=%d started=%d", len(seen), started)
		}
		filtered, err := history.ListWorkflowScheduleOccurrences(ctx, app.ID, firstTenant, "", 100)
		if err != nil || len(filtered) != 6 {
			t.Fatalf("tenant history=%+v err=%v", filtered, err)
		}
		for _, row := range filtered {
			if row.PlatformTenantID != firstTenant {
				t.Fatal("cross-tenant history")
			}
		}
		if _, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, app.ID, firstTenant, deployment.ID, "daily", at.Add(3*time.Minute+40*time.Second)); err != nil || changed {
			t.Fatalf("duplicate minute changed=%t err=%v", changed, err)
		}
		if n, err := history.PruneWorkflowScheduleOccurrences(ctx, at.Add(time.Hour), api.WorkflowScheduleHistoryPruneBatch); err != nil || n != 72 {
			t.Fatalf("prune=%d err=%v", n, err)
		}
	})
}

func TestWorkflowOccurrenceWriteFailureRollsBackRunAndCursor(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, backend Store) {
		store, ok := backend.(*PgStore)
		if !ok {
			return
		}
		ctx := context.Background()
		app, deployment := seedWorkflowSchedule(t, store, "allow")
		at := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		if _, _, err := store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, "ALTER TABLE workflow_schedule_occurrences ADD CONSTRAINT reject_occurrence CHECK (status <> 'started') NOT VALID"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute)); err == nil {
			t.Fatal("expected history write failure")
		}
		if _, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
			t.Fatalf("partial run persisted: %d %v", total, err)
		}
		cursors, err := store.ListWorkflowScheduleCursors(ctx, app.ID)
		if err != nil || len(cursors) != 1 || cursors[0].Status != WorkflowScheduleArmed {
			t.Fatalf("partial cursor persisted: %+v %v", cursors, err)
		}
		if _, err := store.pool.Exec(ctx, "ALTER TABLE workflow_schedule_occurrences DROP CONSTRAINT reject_occurrence"); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := store.AdmitScheduledWorkflow(ctx, app.ID, deployment.ID, "nightly", at.Add(time.Minute)); err != nil || !changed {
			t.Fatalf("retry changed=%t err=%v", changed, err)
		}
	})
}

func TestWorkflowAdmissionCoordinatesUUIDFormsAndLegacyLocks(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, backend Store) {
		store, ok := backend.(*PgStore)
		if !ok {
			return
		}
		app, _ := seedWorkflowSchedule(t, store, "allow")
		parsed, err := uuid.Parse(app.ID)
		if err != nil {
			t.Fatal(err)
		}
		canonical := parsed.String()
		compact := strings.ReplaceAll(canonical, "-", "")
		for _, legacyKey := range []string{compact, canonical} {
			t.Run(legacyKey, func(t *testing.T) {
				ctx := context.Background()
				tx, err := store.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				// Emulate a pre-upgrade writer holding just one historic lock spelling.
				if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, legacyKey); err != nil {
					t.Fatal(err)
				}
				otherKey := canonical
				if legacyKey == canonical {
					otherKey = compact
				}
				blocked, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
				defer cancel()
				run := &WorkflowRun{AppID: otherKey, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"main","run":"report"}]}`)}
				if _, err := store.CreateWorkflowRunAdmitted(blocked, run, 10); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("opposite UUID spelling bypassed legacy lock: %v", err)
				}
			})
		}
		run := &WorkflowRun{AppID: compact, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"main","run":"report"}]}`)}
		if _, err := store.CreateWorkflowRunAdmitted(context.Background(), run, 1); err != nil {
			t.Fatal(err)
		}
		run = &WorkflowRun{AppID: canonical, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"main","run":"report"}]}`)}
		if _, err := store.CreateWorkflowRunAdmitted(context.Background(), run, 1); !errors.Is(err, ErrWorkflowRunQuotaExceeded) {
			t.Fatalf("shared quota=%v", err)
		}
	})
}

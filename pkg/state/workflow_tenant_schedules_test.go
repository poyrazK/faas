package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTenantWorkflowSchedulesAreIsolatedAndPaged(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "tenant-schedule-" + uuid.NewString(),
			Type: AppTypeApp, RAMMB: 256, PlatformTenantRequired: true, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		definitions, err := json.Marshal([]api.WorkflowSpec{{Name: "nightly", Trigger: &api.WorkflowTriggerSpec{
			Type: "schedule", Schedule: "* * * * *", Timezone: "UTC", Overlap: "skip", TenantConfigurable: true,
			Input: json.RawMessage(`{"report":"owner-default"}`),
		}, Steps: []api.WorkflowStepSpec{{Name: "main", Run: "report"}}}})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
			ImageDigest: "sha256:tenant-schedule", Status: DeployPending, Workflows: definitions})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		tenants := store.(PlatformTenantStore)
		var tenantIDs []string
		for range 2 {
			externalRef := "schedule-customer-" + uuid.NewString()
			tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, externalRef, externalRef, 10)
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, externalRef, externalRef)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tenants.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
				t.Fatal(err)
			}
			tenantIDs = append(tenantIDs, tenant.ID)
		}

		schedules := store.(TenantWorkflowScheduleStore)
		firstPage, err := schedules.ListTenantWorkflowScheduleCandidates(ctx, "", "", "", 1)
		if err != nil || len(firstPage) != 1 {
			t.Fatalf("first page=%+v err=%v", firstPage, err)
		}
		secondPage, err := schedules.ListTenantWorkflowScheduleCandidates(ctx, "", firstPage[0].AppID,
			firstPage[0].PlatformTenantID, 1)
		if err != nil || len(secondPage) != 1 || secondPage[0].AppID != app.ID || secondPage[0].PlatformTenantID == firstPage[0].PlatformTenantID {
			t.Fatalf("second page=%+v err=%v", secondPage, err)
		}
		afterPage, err := schedules.ListTenantWorkflowScheduleCandidates(ctx, "", secondPage[0].AppID, secondPage[0].PlatformTenantID, 1)
		if err != nil || len(afterPage) != 0 {
			t.Fatalf("after page=%+v err=%v", afterPage, err)
		}

		candidates, err := schedules.ListTenantWorkflowScheduleCandidates(ctx, "", "", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		defaults, err := schedules.ListTenantWorkflowSchedules(ctx, account.ID, tenantIDs[0], app.ID)
		if err != nil || len(defaults) != 1 || defaults[0].Version != 0 || defaults[0].Customized || !defaults[0].Enabled {
			t.Fatalf("tenant schedule defaults=%+v err=%v", defaults, err)
		}
		paused, err := schedules.UpdateTenantWorkflowSchedule(ctx, account.ID, tenantIDs[0], app.ID, "nightly",
			0, "*/2 * * * *", "UTC", "skip", false)
		if err != nil || paused.Version != 1 || paused.Enabled || !paused.Customized {
			t.Fatalf("pause tenant schedule=%+v err=%v", paused, err)
		}
		if _, err := schedules.UpdateTenantWorkflowSchedule(ctx, account.ID, tenantIDs[0], app.ID, "nightly",
			0, "* * * * *", "UTC", "skip", true); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale schedule update=%v, want ErrConflict", err)
		}
		updatedList, err := schedules.ListTenantWorkflowSchedules(ctx, account.ID, tenantIDs[0], app.ID)
		if err != nil || len(updatedList) != 1 || updatedList[0].Version != 1 || updatedList[0].Enabled || updatedList[0].Schedule != "*/2 * * * *" {
			t.Fatalf("tenant schedule after update=%+v err=%v", updatedList, err)
		}
		at := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
		for _, candidate := range candidates {
			cursor, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, candidate.AppID,
				candidate.PlatformTenantID, candidate.DeploymentID, "nightly", at)
			if candidate.PlatformTenantID == tenantIDs[0] {
				if err != nil || changed {
					t.Fatalf("disabled tenant schedule advanced: cursor=%+v changed=%t err=%v", cursor, changed, err)
				}
				continue
			}
			if err != nil || !changed || cursor.Status != WorkflowScheduleArmed {
				t.Fatalf("arm tenant schedule=%+v changed=%t err=%v", cursor, changed, err)
			}
		}
		for _, candidate := range candidates {
			cursor, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, candidate.AppID,
				candidate.PlatformTenantID, candidate.DeploymentID, "nightly", at.Add(time.Minute))
			if candidate.PlatformTenantID == tenantIDs[0] {
				if err != nil || changed {
					t.Fatalf("disabled tenant schedule advanced: cursor=%+v changed=%t err=%v", cursor, changed, err)
				}
				continue
			}
			if err != nil || !changed || cursor.Status != WorkflowScheduleStarted || cursor.PlatformTenantID != candidate.PlatformTenantID {
				t.Fatalf("start tenant schedule=%+v changed=%t err=%v", cursor, changed, err)
			}
		}
		activeCandidate := candidates[0]
		if activeCandidate.PlatformTenantID == tenantIDs[0] {
			activeCandidate = candidates[1]
		}
		if _, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, activeCandidate.AppID, activeCandidate.PlatformTenantID,
			activeCandidate.DeploymentID, "nightly", at.Add(time.Minute)); err != nil || changed {
			t.Fatalf("duplicate minute admission changed=%t err=%v", changed, err)
		}
		resumed, err := schedules.UpdateTenantWorkflowSchedule(ctx, account.ID, tenantIDs[0], app.ID, "nightly",
			1, "*/2 * * * *", "UTC", "skip", true)
		if err != nil || resumed.Version != 2 || !resumed.Enabled || resumed.Schedule != "*/2 * * * *" {
			t.Fatalf("resume tenant schedule=%+v err=%v", resumed, err)
		}
		var pausedCandidate WorkflowScheduleCandidate
		for _, candidate := range candidates {
			if candidate.PlatformTenantID == tenantIDs[0] {
				pausedCandidate = candidate
			}
		}
		nonmatchingAt := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
		if nonmatchingAt.Minute()%2 == 0 {
			nonmatchingAt = nonmatchingAt.Add(time.Minute)
		}
		if _, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, pausedCandidate.AppID, pausedCandidate.PlatformTenantID,
			pausedCandidate.DeploymentID, "nightly", nonmatchingAt); err != nil || changed {
			t.Fatalf("tenant schedule fired outside its cron: changed=%t err=%v", changed, err)
		}
		resumeAt := nonmatchingAt.Add(time.Minute)
		if cursor, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, pausedCandidate.AppID, pausedCandidate.PlatformTenantID,
			pausedCandidate.DeploymentID, "nightly", resumeAt); err != nil || !changed || cursor.Status != WorkflowScheduleStarted {
			t.Fatalf("resumed tenant schedule=%+v changed=%t err=%v", cursor, changed, err)
		}
		runs, total, err := store.ListWorkflowRuns(ctx, app.ID, ListWorkflowRunsOpts{Limit: 10})
		if err != nil || total != 2 || len(runs) != 2 {
			t.Fatalf("tenant runs=%+v total=%d err=%v", runs, total, err)
		}
		seen := map[string]bool{}
		for _, run := range runs {
			seen[run.PlatformTenantID] = true
		}
		if !seen[tenantIDs[0]] || !seen[tenantIDs[1]] {
			t.Fatalf("scheduled runs did not retain each tenant identity: %+v", seen)
		}
		for _, run := range runs {
			if run.PlatformTenantID != tenantIDs[0] {
				continue
			}
			var snapshot api.WorkflowSpec
			if err := json.Unmarshal(run.DefinitionSnapshot, &snapshot); err != nil || snapshot.Trigger == nil ||
				snapshot.Trigger.Schedule != "*/2 * * * *" || !equalWorkflowJSON(snapshot.Trigger.Input, json.RawMessage(`{"report":"owner-default"}`)) {
				t.Fatalf("tenant run snapshot did not apply the tenant cadence while preserving app input: definition=%s run_input=%s err=%v", run.DefinitionSnapshot, run.Input, err)
			}
		}

		if _, err := tenants.SetPlatformTenantStatus(ctx, account.ID, candidates[0].PlatformTenantID, PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := schedules.AdmitTenantScheduledWorkflow(ctx, app.ID, candidates[0].PlatformTenantID,
			deployment.ID, "nightly", at.Add(2*time.Minute)); err != nil || changed {
			t.Fatalf("suspended tenant admitted a schedule: changed=%t err=%v", changed, err)
		}
		remaining, err := schedules.ListTenantWorkflowScheduleCandidates(ctx, "", "", "", 10)
		if err != nil || len(remaining) != 1 || remaining[0].PlatformTenantID != candidates[1].PlatformTenantID {
			t.Fatalf("active tenant schedule candidates=%+v err=%v", remaining, err)
		}
	})
}

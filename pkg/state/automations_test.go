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

	"github.com/onebox-faas/faas/pkg/api"
)

func automationDraft(name, path, trigger string) json.RawMessage {
	if trigger == "" {
		trigger = `{"type":"manual"}`
	}
	return json.RawMessage(fmt.Sprintf(`{"name":%q,"trigger":%s,"steps":[{"name":"main","path":%q}]}`, name, trigger, path))
}
func mutateForTest(t *testing.T, store AutomationStore, appID, name, action string, version int64, raw json.RawMessage, takeOver bool) Automation {
	t.Helper()
	result, err := store.MutateAutomation(context.Background(), appID, name, AutomationMutation{Action: action, ExpectedVersion: version, Draft: raw, TakeOverManifest: takeOver})
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return result
}
func TestAutomationAuthoring(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, dep := seedWorkflowSchedule(t, store, "allow")
		author := store.(AutomationStore)
		ctx := context.Background()
		raw := automationDraft("nightly", "/dashboard", `{"type":"schedule","schedule":"* * * * *"}`)
		draft := mutateForTest(t, author, app.ID, "nightly", "save", 0, raw, false)
		effective, err := author.EffectiveWorkflowDefinitions(ctx, app.ID, dep.Workflows)
		if err != nil || string(effective) == string(raw) {
			t.Fatal("draft changed effective definitions", err)
		}
		var specs []api.WorkflowSpec
		_ = json.Unmarshal(effective, &specs)
		if len(specs) != 1 || specs[0].Steps[0].Path != "/report" {
			t.Fatalf("draft replaced YAML: %s", effective)
		}
		_, err = author.MutateAutomation(ctx, app.ID, "nightly", AutomationMutation{Action: "publish", ExpectedVersion: draft.Version})
		if !errors.Is(err, ErrAutomationOwnershipConflict) {
			t.Fatalf("takeover without confirmation: %v", err)
		}
		published := mutateForTest(t, author, app.ID, "nightly", "publish", draft.Version, nil, true)
		// Two writers use the same revision: exactly one can commit.
		var successes atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := author.MutateAutomation(ctx, app.ID, "nightly", AutomationMutation{Action: "save", ExpectedVersion: published.Version, Draft: raw})
				if err == nil {
					successes.Add(1)
				} else if !errors.Is(err, ErrAutomationVersionConflict) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatalf("concurrent saves=%d", successes.Load())
		}
		records, err := author.ListAutomations(ctx, app.ID)
		if err != nil || len(records) != 1 {
			t.Fatal(records, err)
		}
		current := records[0]
		if current.PublishedVersion != published.Version {
			t.Fatal("saving mutated publication revision")
		}
		// YAML changes never overwrite a dashboard publication for the same name.
		later := json.RawMessage(`[{"name":"nightly","steps":[{"name":"main","path":"/yaml-new"}]}]`)
		next, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:new", Status: DeployPending, Workflows: later})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		effective, err = author.EffectiveWorkflowDefinitions(ctx, app.ID, later)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(effective, &specs)
		if len(specs) != 1 || specs[0].Steps[0].Path != "/dashboard" {
			t.Fatalf("YAML won: %s", effective)
		}
		// Pause affects automatic admission, while the definition stays present.
		paused, err := author.MutateAutomation(ctx, app.ID, "nightly", AutomationMutation{Action: "enable", ExpectedVersion: current.Version, Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
		paused = mutateForTest(t, author, app.ID, "nightly", "publish", paused.Version, nil, false)
		if paused.Enabled {
			t.Fatal("republishing resumed a paused automation")
		}
		at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
		cursor, changed, err := store.(WorkflowScheduleStore).AdmitScheduledWorkflow(ctx, app.ID, next.ID, "nightly", at)
		if err != nil || changed || cursor.LastRunID != "" {
			t.Fatalf("paused admitted: %+v/%v/%v", cursor, changed, err)
		}
		effective, _ = author.EffectiveWorkflowDefinitions(ctx, app.ID, later)
		_ = json.Unmarshal(effective, &specs)
		if len(specs) != 1 || specs[0].Trigger.Enabled == nil || *specs[0].Trigger.Enabled {
			t.Fatalf("paused missing: %s", effective)
		}
		_, err = author.MutateAutomation(ctx, app.ID, "nightly", AutomationMutation{Action: "delete", ExpectedVersion: paused.Version})
		if !errors.Is(err, ErrAutomationOwnershipConflict) {
			t.Fatalf("implicit restore: %v", err)
		}
		_, err = author.MutateAutomation(ctx, app.ID, "nightly", AutomationMutation{Action: "delete", ExpectedVersion: paused.Version, RestoreManifest: true})
		if err != nil {
			t.Fatal(err)
		}
		effective, _ = author.EffectiveWorkflowDefinitions(ctx, app.ID, later)
		_ = json.Unmarshal(effective, &specs)
		if specs[0].Steps[0].Path != "/yaml-new" {
			t.Fatalf("restore YAML: %s", effective)
		}
		recreated := mutateForTest(t, author, app.ID, "nightly", "save", 0, raw, false)
		if recreated.Version <= paused.Version {
			t.Fatalf("revision reused after deletion: %d <= %d", recreated.Version, paused.Version)
		}
	})
}
func TestAutomationValidationQuotaAndEvents(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, dep := seedWorkflowSchedule(t, store, "allow")
		author := store.(AutomationStore)
		ctx := context.Background()
		invalid := mutateForTest(t, author, app.ID, "invalid", "save", 0, json.RawMessage(`{"name":"invalid","steps":[]}`), false)
		_, err := author.MutateAutomation(ctx, app.ID, "invalid", AutomationMutation{Action: "publish", ExpectedVersion: invalid.Version})
		if !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("invalid published: %v", err)
		}
		raw := automationDraft("invoice", "/invoice", `{"type":"event","source":"billing","event_type":"invoice.paid"}`)
		event := mutateForTest(t, author, app.ID, "invoice", "save", 0, raw, false)
		event = mutateForTest(t, author, app.ID, "invoice", "publish", event.Version, nil, false)
		_, err = author.MutateAutomation(ctx, app.ID, "overflow", AutomationMutation{Action: "save", Draft: automationDraft("overflow", "/x", "")})
		var quota *AutomationQuotaError
		if !errors.As(err, &quota) || quota.Observed != 4 || quota.Limit != 3 {
			t.Fatalf("quota: %v", err)
		}
		recipients, err := store.(EventWorkflowStore).ListMatchingEventWorkflows(ctx, app.AccountID, "billing", "invoice.paid", "", 100)
		if err != nil || len(recipients) != 1 || recipients[0].DeploymentID != dep.ID {
			t.Fatalf("event recipients=%+v err=%v", recipients, err)
		}
		var definition api.WorkflowSpec
		_ = json.Unmarshal(recipients[0].Workflow, &definition)
		if definition.Steps[0].Path != "/invoice" {
			t.Fatalf("event snapshot=%s", recipients[0].Workflow)
		}
		_, err = author.MutateAutomation(ctx, app.ID, "invoice", AutomationMutation{Action: "enable", ExpectedVersion: event.Version, Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
		recipients, err = store.(EventWorkflowStore).ListMatchingEventWorkflows(ctx, app.AccountID, "billing", "invoice.paid", "", 100)
		if err != nil || len(recipients) != 0 {
			t.Fatalf("paused event recipients=%+v err=%v", recipients, err)
		}
	})
}

func TestAutomationDeploymentQuota(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedWorkflowSchedule(t, store, "allow")
		author := store.(AutomationStore)
		ctx := context.Background()
		// A build starts with two YAML names; then two dashboard drafts reserve
		// additional names before it is ready. Cutover must reject the union of four.
		raw := json.RawMessage(`[{"name":"nightly","steps":[{"name":"main","path":"/"}]},{"name":"from-build","steps":[{"name":"main","path":"/"}]}]`)
		pending, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:pending", Status: DeployPending, Workflows: raw})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"draft-a", "draft-b"} {
			mutateForTest(t, author, app.ID, name, "save", 0, automationDraft(name, "/", ""), false)
		}
		err = store.MarkDeploymentLive(ctx, pending.ID)
		if problem := api.AsProblem(err); problem == nil || problem.Code != api.CodePlanWorkflowsQuota {
			t.Fatalf("cutover bypassed quota: %v", err)
		}
		_, err = store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:new", Status: DeployPending, Workflows: raw})
		if problem := api.AsProblem(err); problem == nil || problem.Code != api.CodePlanWorkflowsQuota {
			t.Fatalf("deploy bypassed quota: %v", err)
		}
	})
}
func TestAutomationPublicationRollback(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		pg, ok := store.(*PgStore)
		if !ok {
			t.Skip("Postgres transactional fault injection")
		}
		app, dep := seedWorkflowSchedule(t, store, "allow")
		ctx := context.Background()
		author := store.(AutomationStore)
		draft := mutateForTest(t, author, app.ID, "nightly", "save", 0, automationDraft("nightly", "/dashboard", `{"type":"schedule","schedule":"* * * * *"}`), false)
		_, _, err := store.(WorkflowScheduleStore).AdmitScheduledWorkflow(ctx, app.ID, dep.ID, "nightly", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		_, err = pg.pool.Exec(ctx, `CREATE FUNCTION reject_automation_cursor_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected cursor delete failure'; END $$; CREATE TRIGGER reject_automation_cursor_delete BEFORE DELETE ON workflow_schedule_cursors FOR EACH ROW EXECUTE FUNCTION reject_automation_cursor_delete()`)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pg.pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_automation_cursor_delete ON workflow_schedule_cursors; DROP FUNCTION IF EXISTS reject_automation_cursor_delete()`)
		})
		_, err = author.MutateAutomation(ctx, app.ID, "nightly", AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, TakeOverManifest: true})
		if err == nil {
			t.Fatal("publication committed after cursor failure")
		}
		rows, err := author.ListAutomations(ctx, app.ID)
		if err != nil || len(rows) != 1 || rows[0].Version != draft.Version || len(rows[0].Published) != 0 {
			t.Fatalf("publication leaked: %+v/%v", rows, err)
		}
	})
}

func TestAutomationAcceptedEventSnapshot(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedEventWorkflow(t, store)
		author := store.(AutomationStore)
		ctx := context.Background()
		raw := automationDraft("paid", "/dashboard-old", `{"type":"event","source":"billing.*","event_type":"invoice.paid"}`)
		saved := mutateForTest(t, author, app.ID, "paid", "save", 0, raw, false)
		published := mutateForTest(t, author, app.ID, "paid", "publish", saved.Version, nil, true)
		work := publishWorkflowEvent(t, store, app, "before-republication")
		if len(work.RecipientSnapshot) != 1 {
			t.Fatalf("captured recipients=%+v", work.RecipientSnapshot)
		}
		saved = mutateForTest(t, author, app.ID, "paid", "save", published.Version, automationDraft("paid", "/dashboard-new", `{"type":"event","source":"billing.*","event_type":"invoice.paid"}`), false)
		published = mutateForTest(t, author, app.ID, "paid", "publish", saved.Version, nil, false)
		_, err := author.MutateAutomation(ctx, app.ID, "paid", AutomationMutation{Action: "enable", ExpectedVersion: published.Version, Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.GetWorkflowRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(run.DefinitionSnapshot, &definition); err != nil || definition.Steps[0].Path != "/dashboard-old" {
			t.Fatalf("accepted event changed: %s/%v", run.DefinitionSnapshot, err)
		}
	})
}

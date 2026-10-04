package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func seedEventWorkflow(t *testing.T, store Store) (App, Deployment) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "event-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: DeployPending, Workflows: json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","filter":{"data":{"amount":{"$gt":100}}}},"steps":[{"name":"main","path":"/paid"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	return app, deployment
}

func publishWorkflowEvent(t *testing.T, store Store, app App, id string) *PublishedEventWork {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "billing.stripe", "type": "invoice.paid",
		"accountid": canonicalMemUUID(app.AccountID), "datacontenttype": "application/json", "time": time.Now().UTC(), "data": map[string]any{"amount": 150}})
	if err := store.AppendEvent(context.Background(), "apid", "event.published", &app.AccountID, payload); err != nil {
		t.Fatal(err)
	}
	work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.(PublishedEventWorkStore).FinishPublishedEvent(context.Background(), work.ID, work.ClaimToken, nil)
	})
	return work
}

func TestEventWorkflowAdmission(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		t.Run("candidate paging and default scope", func(t *testing.T) {
			app, _ := seedEventWorkflow(t, store)
			ctx := context.Background()
			definitions := json.RawMessage(`[
 {"name":"one","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","enabled":null},"steps":[{"name":"main","path":"/one"}]},
 {"name":"two","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/two"}]},
 {"name":"off","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","enabled":false},"steps":[{"name":"main","path":"/off"}]}]`)
			var live Deployment
			for _, scope := range []string{"default", "preview"} {
				deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:def", Status: DeployPending, Scope: scope, Workflows: definitions})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
					t.Fatal(err)
				}
				if scope == "default" {
					live = deployment
				}
			}
			starts := store.(EventWorkflowStore)
			after := ""
			count := 0
			for {
				page, err := starts.ListMatchingEventWorkflows(ctx, app.AccountID, "billing.stripe", "invoice.paid", after, 1)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				count++
				if count > 2 || page[0].DeploymentID != live.ID || page[0].ID <= after {
					t.Fatalf("unexpected candidate %+v", page[0])
				}
				after = page[0].ID
			}
			if count != 2 {
				t.Fatalf("candidate count=%d", count)
			}
			if page, err := starts.ListMatchingEventWorkflows(ctx, uuid.NewString(), "billing.stripe", "invoice.paid", "", 100); err != nil || len(page) != 0 {
				t.Fatalf("foreign candidates=%+v err=%v", page, err)
			}
		})
		t.Run("account and tenant guards", func(t *testing.T) {
			for _, test := range []struct {
				name  string
				block func(context.Context, App) error
			}{
				{"suspended", func(ctx context.Context, app App) error {
					return store.UpdateAccountStatus(ctx, app.AccountID, AccountSuspended)
				}},
				{"free", func(ctx context.Context, app App) error {
					return store.UpdateAccountPlan(ctx, app.AccountID, api.PlanFree)
				}},
				{"tenant", func(ctx context.Context, app App) error {
					value := true
					_, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{PlatformTenantRequired: &value, SetPlatformTenantRequired: true})
					return err
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					app, _ := seedEventWorkflow(t, store)
					work := publishWorkflowEvent(t, store, app, uuid.NewString())
					if err := test.block(context.Background(), app); err != nil {
						t.Fatal(err)
					}
					if _, err := store.(EventWorkflowStore).AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); !errors.Is(err, ErrWorkflowEventTargetUnavailable) {
						t.Fatalf("guard=%v", err)
					}
					if page, err := store.(EventWorkflowStore).ListMatchingEventWorkflows(context.Background(), app.AccountID, "billing.stripe", "invoice.paid", "", 100); err != nil || len(page) != 0 {
						t.Fatalf("blocked captures=%+v err=%v", page, err)
					}
				})
			}
		})
		t.Run("snapshot and concurrent recovery", func(t *testing.T) {
			app, original := seedEventWorkflow(t, store)
			work := publishWorkflowEvent(t, store, app, uuid.NewString())
			if len(work.RecipientSnapshot) != 1 || work.RecipientSnapshot[0].DeploymentID != original.ID {
				t.Fatalf("snapshot=%+v", work.RecipientSnapshot)
			}
			// A replacement without event triggers cannot rewrite an accepted event.
			replacement, err := store.CreateDeployment(context.Background(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:def", Status: DeployPending, Workflows: json.RawMessage(`[]`)})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(context.Background(), replacement.ID); err != nil {
				t.Fatal(err)
			}
			starts := store.(EventWorkflowStore)
			var group sync.WaitGroup
			ids := make(chan string, 16)
			for range 16 {
				group.Add(1)
				go func() {
					defer group.Done()
					id, err := starts.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
					if err != nil {
						t.Error(err)
					}
					ids <- id
				}()
			}
			group.Wait()
			close(ids)
			var runID string
			for id := range ids {
				if runID == "" {
					runID = id
				}
				if id != runID {
					t.Fatal("recovery admitted different runs")
				}
			}
			run, err := store.GetWorkflowRun(context.Background(), runID)
			if err != nil || !equalWorkflowJSON(run.Input, work.Payload) || !equalWorkflowJSON(run.DefinitionSnapshot, work.RecipientSnapshot[0].Workflow) {
				t.Fatalf("run=%+v err=%v", run, err)
			}
			if err := store.MarkWorkflowRunStatus(context.Background(), runID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SweepExpiredWorkflowRuns(context.Background(), 0); err != nil {
				t.Fatal(err)
			}
			if id, err := starts.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err != nil || id != "" {
				t.Fatalf("retained receipt id=%s err=%v", id, err)
			}
			if _, total, err := store.ListWorkflowRuns(context.Background(), app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
				t.Fatalf("duplicate after retention: total=%d err=%v", total, err)
			}
			if _, err := starts.AdmitEventWorkflow(context.Background(), work.ID, uuid.NewString(), work.RecipientSnapshot[0].ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale claim=%v", err)
			}
		})
		t.Run("quota and maintenance recover", func(t *testing.T) {
			app, _ := seedEventWorkflow(t, store)
			work := publishWorkflowEvent(t, store, app, uuid.NewString())
			starts := store.(EventWorkflowStore)
			value := true
			if _, err := store.UpdateApp(context.Background(), app.ID, UpdateAppParams{MaintenanceMode: &value, SetMaintenanceMode: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := starts.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); !errors.Is(err, ErrWorkflowEventTargetUnavailable) {
				t.Fatalf("maintenance=%v", err)
			}
			value = false
			if _, err := store.UpdateApp(context.Background(), app.ID, UpdateAppParams{MaintenanceMode: &value, SetMaintenanceMode: true}); err != nil {
				t.Fatal(err)
			}
			max := api.PlanHobby.WorkflowMaxConcurrentRuns()
			var manual *WorkflowRun
			for range max {
				manual = &WorkflowRun{AppID: app.ID, WorkflowName: "manual", DefinitionSnapshot: json.RawMessage(`{}`)}
				if _, err := store.CreateWorkflowRunAdmitted(context.Background(), manual, max); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := starts.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); !errors.Is(err, ErrWorkflowRunQuotaExceeded) {
				t.Fatalf("quota=%v", err)
			}
			if err := store.MarkWorkflowRunStatus(context.Background(), manual.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := starts.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err != nil {
				t.Fatal(err)
			}
		})
		t.Run("receipt rollback", func(t *testing.T) {
			pg, ok := store.(*PgStore)
			if !ok {
				return
			}
			app, _ := seedEventWorkflow(t, store)
			work := publishWorkflowEvent(t, store, app, uuid.NewString())
			if _, err := pg.pool.Exec(context.Background(), "ALTER TABLE workflow_event_receipts ADD CONSTRAINT reject_receipt CHECK (false) NOT VALID"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err == nil {
				t.Fatal("expected receipt failure")
			}
			if _, total, err := store.ListWorkflowRuns(context.Background(), app.ID, ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 0 {
				t.Fatalf("rollback total=%d err=%v", total, err)
			}
			if _, err := pg.pool.Exec(context.Background(), "ALTER TABLE workflow_event_receipts DROP CONSTRAINT reject_receipt"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.AdmitEventWorkflow(context.Background(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err != nil {
				t.Fatal(err)
			}
		})
	})
}

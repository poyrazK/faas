package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTenantEventWorkflowAdmissionAndLinkRecheckIntegratedRouting(t *testing.T) {
	for _, independent := range []bool{false, true} {
		t.Run(fmt.Sprintf("independent=%t", independent), func(t *testing.T) {
			testTenantEventWorkflowAdmissionAndLinkRecheck(t, independent)
		})
	}
}

func testTenantEventWorkflowAdmissionAndLinkRecheck(t *testing.T, independent bool) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "tenant-event-" + uuid.NewString(), Type: AppTypeApp,
			RAMMB: 256, PlatformTenantRequired: true})
		if err != nil {
			t.Fatal(err)
		}
		definitions, _ := json.Marshal([]api.WorkflowSpec{{Name: "invoice-paid", Trigger: &api.WorkflowTriggerSpec{
			Type: "event", Source: "billing.*", EventType: "invoice.paid",
		}, Steps: []api.WorkflowStepSpec{{Name: "process", Path: "/process"}}}})
		deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
			ImageDigest: "sha256:abc", Status: DeployPending, Workflows: definitions})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		tenantStore := store.(PlatformTenantStore)
		tenant, _, err := tenantStore.CreatePlatformTenant(ctx, account.ID, "tenant-events", "Tenant events", 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "tenant-events-consumer", "Tenant events consumer")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenantStore.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePlatformTenantAppBinding(ctx, tenantStore, account.ID, tenant.ID, app.ID); err != nil {
			t.Fatalf("seeded tenant app binding is not active: %v", err)
		}
		publish := func(id string) error {
			t.Helper()
			payload, err := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "billing.acme", "type": "invoice.paid",
				"accountid": account.ID, "appid": app.ID, "platformtenantid": tenant.ID, "tenanteventid": id,
				"datacontenttype": "application/json", "time": time.Now().UTC(), "data": map[string]any{"amount": 125}})
			if err != nil {
				return err
			}
			return store.(TenantPublishedEventStore).AppendTenantPublishedEvent(ctx, "apid", account.ID, tenant.ID, app.ID, payload, nil)
		}
		if err := publish("tenant-event-1"); err != nil {
			t.Fatal(err)
		}
		work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil || work == nil || len(work.RecipientSnapshot) != 1 {
			t.Fatalf("claim tenant event=%+v err=%v", work, err)
		}
		recipient := work.RecipientSnapshot[0]
		if recipient.PlatformTenantID != tenant.ID || recipient.AppID != app.ID || recipient.ID != workflowTenantEventRecipientID(app.ID, tenant.ID, "invoice-paid") {
			t.Fatalf("tenant event recipient=%+v", recipient)
		}
		admit := func(work *PublishedEventWork) (string, error) {
			if !independent {
				return store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
			}
			routing := store.(PublishedEventRecipientWorkStore)
			if err := routing.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
				return "", err
			}
			claimed, err := routing.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				return "", err
			}
			result, err := store.(EventWorkflowRecipientAdmissionStore).AdmitEventWorkflowRecipient(ctx,
				PublishedEventRoutingClaim{OutboxID: work.ID, SubscriptionID: claimed.Recipient.ID, ClaimToken: claimed.ClaimToken, Generation: claimed.Generation})
			return result.RunID, err
		}
		runID, err := admit(work)
		if err != nil || runID == "" {
			t.Fatalf("admit tenant workflow run=%q err=%v", runID, err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil || run.PlatformTenantID != tenant.ID || run.WorkflowName != "invoice-paid" {
			t.Fatalf("tenant run=%+v err=%v", run, err)
		}
		receipt, err := store.(EventReceiptStore).EventReceipt(ctx, account.ID, "billing.acme", "tenant-event-1", EventReceiptCursor{}, 20)
		if err != nil || receipt.AppID != app.ID || receipt.PlatformTenantID != tenant.ID || receipt.ClientEventID != "tenant-event-1" ||
			len(receipt.Recipients) != 1 || receipt.Recipients[0].WorkflowRunID != runID || receipt.Recipients[0].WorkflowRunStatus != string(run.Status) {
			t.Fatalf("tenant receipt=%+v err=%v", receipt, err)
		}
		if !independent {
			if err := store.(PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
				t.Fatal(err)
			}
		}

		if err := publish("tenant-event-2"); err != nil {
			t.Fatal(err)
		}
		revocationWork, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil || revocationWork == nil {
			t.Fatalf("claim before tenant revocation=%+v err=%v", revocationWork, err)
		}
		if _, err := tenantStore.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		if _, err := admit(revocationWork); !errors.Is(err, ErrWorkflowEventTargetUnavailable) {
			t.Fatalf("admission after tenant suspension=%v", err)
		}
		if err := publish("tenant-event-3"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("publish after tenant suspension=%v", err)
		}
	})
}

func TestTenantEventWorkflowAdmissionAndLinkRecheck(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "tenant-event-" + uuid.NewString(), Type: AppTypeApp,
			RAMMB: 256, PlatformTenantRequired: true})
		if err != nil {
			t.Fatal(err)
		}
		definitions, _ := json.Marshal([]api.WorkflowSpec{{Name: "invoice-paid", Trigger: &api.WorkflowTriggerSpec{
			Type: "event", Source: "billing.*", EventType: "invoice.paid",
		}, Steps: []api.WorkflowStepSpec{{Name: "process", Path: "/process"}}}})
		deployment, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage,
			ImageDigest: "sha256:abc", Status: DeployPending, Workflows: definitions})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		tenantStore := store.(PlatformTenantStore)
		tenant, _, err := tenantStore.CreatePlatformTenant(ctx, account.ID, "tenant-events", "Tenant events", 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "tenant-events-consumer", "Tenant events consumer")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenantStore.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePlatformTenantAppBinding(ctx, tenantStore, account.ID, tenant.ID, app.ID); err != nil {
			t.Fatalf("seeded tenant app binding is not active: %v", err)
		}
		publish := func(id string) error {
			t.Helper()
			payload, err := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "billing.acme", "type": "invoice.paid",
				"accountid": account.ID, "appid": app.ID, "platformtenantid": tenant.ID, "tenanteventid": id,
				"datacontenttype": "application/json", "time": time.Now().UTC(), "data": map[string]any{"amount": 125}})
			if err != nil {
				return err
			}
			return store.(TenantPublishedEventStore).AppendTenantPublishedEvent(ctx, "apid", account.ID, tenant.ID, app.ID, payload, nil)
		}
		if err := publish("tenant-event-1"); err != nil {
			t.Fatal(err)
		}
		work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil || work == nil || len(work.RecipientSnapshot) != 1 {
			t.Fatalf("claim tenant event=%+v err=%v", work, err)
		}
		recipient := work.RecipientSnapshot[0]
		if recipient.PlatformTenantID != tenant.ID || recipient.AppID != app.ID || recipient.ID != workflowTenantEventRecipientID(app.ID, tenant.ID, "invoice-paid") {
			t.Fatalf("tenant event recipient=%+v", recipient)
		}
		runID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, recipient.ID)
		if err != nil || runID == "" {
			t.Fatalf("admit tenant workflow run=%q err=%v", runID, err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil || run.PlatformTenantID != tenant.ID || run.WorkflowName != "invoice-paid" {
			t.Fatalf("tenant run=%+v err=%v", run, err)
		}
		receipt, err := store.(EventReceiptStore).EventReceipt(ctx, account.ID, "billing.acme", "tenant-event-1", EventReceiptCursor{}, 20)
		if err != nil || receipt.AppID != app.ID || receipt.PlatformTenantID != tenant.ID || receipt.ClientEventID != "tenant-event-1" ||
			len(receipt.Recipients) != 1 || receipt.Recipients[0].WorkflowRunID != runID || receipt.Recipients[0].WorkflowRunStatus != string(run.Status) {
			t.Fatalf("tenant receipt=%+v err=%v", receipt, err)
		}
		if err := store.(PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}

		if err := publish("tenant-event-2"); err != nil {
			t.Fatal(err)
		}
		revocationWork, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil || revocationWork == nil {
			t.Fatalf("claim before tenant revocation=%+v err=%v", revocationWork, err)
		}
		if _, err := tenantStore.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		if _, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, revocationWork.ID, revocationWork.ClaimToken, revocationWork.RecipientSnapshot[0].ID); !errors.Is(err, ErrWorkflowEventTargetUnavailable) {
			t.Fatalf("admission after tenant suspension=%v", err)
		}
		if err := publish("tenant-event-3"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("publish after tenant suspension=%v", err)
		}
	})
}

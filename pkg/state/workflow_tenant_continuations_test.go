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

func TestTenantWorkflowContinuationsRequireRunOwnershipAndActiveLink(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "tenant-continuation-"+uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "tenant-continuation-" + uuid.NewString(), Type: AppTypeApp, RAMMB: 256, PlatformTenantRequired: true})
		if err != nil {
			t.Fatal(err)
		}
		tenants := store.(PlatformTenantStore)
		tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, "customer-"+uuid.NewString(), "Customer", 10)
		if err != nil {
			t.Fatal(err)
		}
		otherTenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, "other-"+uuid.NewString(), "Other customer", 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "customer-"+uuid.NewString(), "Customer")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenants.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}

		continuations := store.(TenantWorkflowContinuationStore)
		eventSpec, err := json.Marshal(api.WorkflowSpec{Name: "approval", Steps: []api.WorkflowStepSpec{{Name: "wait", WaitForEvent: "approved", Timeout: time.Hour}}})
		if err != nil {
			t.Fatal(err)
		}
		eventRun := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenant.ID, WorkflowName: "approval", DefinitionSnapshot: eventSpec}
		if err := store.CreateWorkflowRun(ctx, eventRun); err != nil {
			t.Fatal(err)
		}
		event := &WorkflowEvent{ID: uuid.NewString(), RunID: eventRun.ID, EventName: "approved", Payload: json.RawMessage(`{"ok":true}`)}
		if err := continuations.InsertTenantWorkflowEvent(ctx, otherTenant.ID, event); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("foreign tenant event = %v, want not found", err)
		}
		if err := continuations.InsertTenantWorkflowEvent(ctx, tenant.ID, event); err != nil {
			t.Fatalf("active tenant event = %v", err)
		}
		if err := continuations.InsertTenantWorkflowEvent(ctx, tenant.ID, event); err != nil {
			t.Fatalf("idempotent tenant event replay = %v", err)
		}
		events, err := store.GetWorkflowEventsForRun(ctx, eventRun.ID)
		if err != nil || len(events) != 1 {
			t.Fatalf("events after replay = %d, err=%v; want one", len(events), err)
		}

		callbackSpec, err := json.Marshal(api.WorkflowSpec{Name: "callback", Steps: []api.WorkflowStepSpec{{Name: "await", WaitForCallback: true, Timeout: time.Hour}}})
		if err != nil {
			t.Fatal(err)
		}
		callbackRun := &WorkflowRun{AppID: app.ID, PlatformTenantID: tenant.ID, WorkflowName: "callback", DefinitionSnapshot: callbackSpec}
		if err := store.CreateWorkflowRun(ctx, callbackRun); err != nil {
			t.Fatal(err)
		}
		callbackID := api.WorkflowCallbackID(callbackRun.ID, "await")
		callbackEvent := api.WorkflowCallbackEventName(callbackRun.ID, "await")
		payload := json.RawMessage(`{"approved":true}`)
		if _, err := continuations.CompleteTenantWorkflowCallback(ctx, otherTenant.ID, callbackRun.ID, "await", callbackEvent, callbackID, time.Hour, payload); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("foreign tenant callback = %v, want not found", err)
		}
		if duplicate, err := continuations.CompleteTenantWorkflowCallback(ctx, tenant.ID, callbackRun.ID, "await", callbackEvent, callbackID, time.Hour, payload); err != nil || duplicate {
			t.Fatalf("first tenant callback = (duplicate %t, err %v)", duplicate, err)
		}
		if duplicate, err := continuations.CompleteTenantWorkflowCallback(ctx, tenant.ID, callbackRun.ID, "await", callbackEvent, callbackID, time.Hour, payload); err != nil || !duplicate {
			t.Fatalf("tenant callback replay = (duplicate %t, err %v), want duplicate", duplicate, err)
		}
		if _, err := continuations.CompleteTenantWorkflowCallback(ctx, tenant.ID, callbackRun.ID, "await", callbackEvent, callbackID, time.Hour, json.RawMessage(`{"approved":false}`)); !errors.Is(err, ErrConflict) {
			t.Fatalf("tenant callback payload conflict = %v, want conflict", err)
		}

		if _, err := store.RevokeAPIConsumer(ctx, account.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		if err := continuations.InsertTenantWorkflowEvent(ctx, tenant.ID, &WorkflowEvent{ID: uuid.NewString(), RunID: eventRun.ID, EventName: "approved", Payload: json.RawMessage(`{}`)}); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("event after tenant link revocation = %v, want not found", err)
		}
		if _, err := continuations.CompleteTenantWorkflowCallback(ctx, tenant.ID, callbackRun.ID, "await", callbackEvent, uuid.NewString(), time.Hour, payload); !errors.Is(err, ErrWorkflowRunNotFound) {
			t.Fatalf("callback after tenant link revocation = %v, want not found", err)
		}
	})
}

package state

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreWorkflowFinishedWebhookOutboxSnapshotsTerminalRun(t *testing.T) {
	store, ctx, account, app := webhookFixture(t)
	hookInput := memSampleWebhook(account.ID, app.ID)
	hookInput.EventFilter = []string{string(AppWebhookEventWorkflowFinished)}
	hook, err := store.CreateAppWebhook(ctx, hookInput)
	if err != nil {
		t.Fatal(err)
	}
	disabledInput := memSampleWebhook(account.ID, app.ID)
	disabledInput.EventFilter = []string{string(AppWebhookEventWorkflowFinished)}
	disabledInput.Enabled = false
	disabled, err := store.CreateAppWebhook(ctx, disabledInput)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedInput := memSampleWebhook(account.ID, app.ID)
	unrelatedInput.EventFilter = []string{string(AppWebhookEventAppWoken)}
	unrelated, err := store.CreateAppWebhook(ctx, unrelatedInput)
	if err != nil {
		t.Fatal(err)
	}

	run := &WorkflowRun{
		AppID: app.ID, WorkflowName: "invoice-receipt",
		DefinitionSnapshot: json.RawMessage(`{"name":"invoice-receipt","steps":[]}`),
	}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	privateOutput := json.RawMessage(`{"private":"output"}`)
	privateError := "private executor details"
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusFailed, privateOutput, &privateError); err != nil {
		t.Fatal(err)
	}
	// An idempotent repeat of the terminal status must not emit a second event.
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusFailed, nil, nil); err != nil {
		t.Fatal(err)
	}

	lateInput := memSampleWebhook(account.ID, app.ID)
	lateInput.EventFilter = []string{string(AppWebhookEventWorkflowFinished)}
	late, err := store.CreateAppWebhook(ctx, lateInput)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relayed workflow.finished events = %d, %v", n, err)
	}

	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != AppWebhookEventWorkflowFinished {
		t.Fatalf("workflow.finished deliveries = %+v, %v", deliveries, err)
	}
	var payload api.WorkflowFinishedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AppID != app.ID || payload.RunID != run.ID || payload.WorkflowName != run.WorkflowName ||
		payload.Status != WorkflowRunStatusFailed || payload.FinishedAt.IsZero() || payload.ResumeCount != 0 {
		t.Fatalf("workflow.finished payload = %+v", payload)
	}
	if strings.Contains(string(deliveries[0].Payload), "private") || strings.Contains(string(deliveries[0].Payload), "executor details") {
		t.Fatalf("workflow.finished payload leaked run data: %s", deliveries[0].Payload)
	}
	for _, recipient := range []AppWebhook{disabled, unrelated, late} {
		rows, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, recipient.ID, 10, "")
		if err != nil || len(rows) != 0 {
			t.Errorf("ineligible recipient %s deliveries = %+v, %v", recipient.ID, rows, err)
		}
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 0 {
		t.Fatalf("repeat relay = %d, %v", n, err)
	}
}

func TestMemStoreWorkflowFinishedWebhookIncludesCancellation(t *testing.T) {
	store, ctx, account, app := webhookFixture(t)
	hook := memSampleWebhook(account.ID, app.ID)
	hook.EventFilter = []string{string(AppWebhookEventWorkflowFinished)}
	created, err := store.CreateAppWebhook(ctx, hook)
	if err != nil {
		t.Fatal(err)
	}
	run := &WorkflowRun{AppID: app.ID, WorkflowName: "cancel-me", DefinitionSnapshot: json.RawMessage(`{"name":"cancel-me","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelWorkflowRun(ctx, run.ID, "requested by customer"); err != nil {
		t.Fatal(err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relayed cancellation event = %d, %v", n, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, created.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != AppWebhookEventWorkflowFinished {
		t.Fatalf("cancellation deliveries = %+v, %v", deliveries, err)
	}
	var payload api.WorkflowFinishedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil || payload.Status != WorkflowRunStatusFailed {
		t.Fatalf("cancellation payload = %+v, %v", payload, err)
	}
}

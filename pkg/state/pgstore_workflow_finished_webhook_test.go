package state_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgWorkflowFinishedWebhookOutboxIsTransactionalAndSnapshotBased(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	hookInput := pgSampleWebhook(accountID, appID)
	hookInput.EventFilter = []string{string(state.AppWebhookEventWorkflowFinished)}
	hook, err := store.CreateAppWebhook(ctx, hookInput)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{
		AppID: appID, WorkflowName: "invoice-receipt",
		Input:              json.RawMessage(`{"private":"input"}`),
		DefinitionSnapshot: json.RawMessage(`{"name":"invoice-receipt","steps":[]}`),
	}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	privateOutput := json.RawMessage(`{"private":"output"}`)
	privateError := "private executor details"
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, privateOutput, &privateError); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `
		select payload from app_webhook_event_outbox
		where event = 'workflow.finished' and payload->>'run_id' = $1`, run.ID).Scan(&raw); err != nil {
		t.Fatalf("committed workflow.finished outbox event: %v", err)
	}
	var payload api.WorkflowFinishedWebhookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AppID != appID || payload.RunID != run.ID || payload.WorkflowName != run.WorkflowName ||
		payload.Status != state.WorkflowRunStatusSucceeded || payload.FinishedAt.IsZero() || payload.ResumeCount != 0 {
		t.Fatalf("workflow.finished payload = %+v", payload)
	}
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "executor details") {
		t.Fatalf("workflow.finished payload leaked run data: %s", raw)
	}
	deadRun := &state.WorkflowRun{AppID: appID, WorkflowName: "dead-run", DefinitionSnapshot: json.RawMessage(`{"name":"dead-run","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, deadRun); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, deadRun.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	cancelledRun := &state.WorkflowRun{AppID: appID, WorkflowName: "cancelled-run", DefinitionSnapshot: json.RawMessage(`{"name":"cancelled-run","steps":[]}`)}
	if err := store.CreateWorkflowRun(ctx, cancelledRun); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelWorkflowRun(ctx, cancelledRun.ID, "requested by customer"); err != nil {
		t.Fatal(err)
	}
	var statusCount int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_event_outbox where app_id=$1 and event='workflow.finished'`, appID).Scan(&statusCount); err != nil || statusCount != 3 {
		t.Fatalf("terminal workflow outcome events = %d, %v", statusCount, err)
	}

	lateInput := pgSampleWebhook(accountID, appID)
	lateInput.EventFilter = []string{string(state.AppWebhookEventWorkflowFinished)}
	late, err := store.CreateAppWebhook(ctx, lateInput)
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	if n, err := restarted.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 3 {
		t.Fatalf("relay after restart = %d, %v", n, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 0 {
		t.Fatalf("repeat relay = %d, %v", n, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("workflow.finished deliveries = %+v, %v", deliveries, err)
	}
	statuses := map[string]bool{}
	for _, delivery := range deliveries {
		if delivery.Event != state.AppWebhookEventWorkflowFinished {
			t.Errorf("unexpected delivery event = %q", delivery.Event)
		}
		var event api.WorkflowFinishedWebhookPayload
		if err := json.Unmarshal(delivery.Payload, &event); err != nil {
			t.Fatal(err)
		}
		statuses[event.Status] = true
	}
	for _, status := range []string{state.WorkflowRunStatusSucceeded, state.WorkflowRunStatusFailed, state.WorkflowRunStatusDead} {
		if !statuses[status] {
			t.Errorf("missing workflow.finished status %q in %+v", status, statuses)
		}
	}
	lateDeliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, late.ID, 10, "")
	if err != nil || len(lateDeliveries) != 0 {
		t.Fatalf("late webhook received an earlier outcome: %+v, %v", lateDeliveries, err)
	}

	// The source update and outbox insert roll back together.
	rolledBack := &state.WorkflowRun{
		AppID: appID, WorkflowName: "rolled-back",
		DefinitionSnapshot: json.RawMessage(`{"name":"rolled-back","steps":[]}`),
	}
	if err := store.CreateWorkflowRun(ctx, rolledBack); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update workflow_runs set status='dead',finished_at=now() where id=$1`, rolledBack.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var rolledBackEvents int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_event_outbox where payload->>'run_id'=$1`, rolledBack.ID).Scan(&rolledBackEvents); err != nil || rolledBackEvents != 0 {
		t.Fatalf("rolled-back events = %d, %v", rolledBackEvents, err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	var duplicateEvents int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_event_outbox where payload->>'run_id'=$1`, run.ID).Scan(&duplicateEvents); err != nil || duplicateEvents != 0 {
		t.Fatalf("duplicate terminal transition events = %d, %v", duplicateEvents, err)
	}
}

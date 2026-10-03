package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type eventWorkflowProgressFailure struct {
	*state.MemStore
	fail bool
}

func (s *eventWorkflowProgressFailure) RecordPublishedEventRecipientProgress(ctx context.Context, id int64, token, recipientID string, progress state.PublishedEventRecipientProgress) error {
	if s.fail && progress.State == state.PublishedEventRecipientEnqueued {
		s.fail = false
		return errors.New("checkpoint interrupted")
	}
	return s.MemStore.RecordPublishedEventRecipientProgress(ctx, id, token, recipientID, progress)
}

func TestEventWorkflowStartsThroughFanoutAndRecoversCheckpoint(t *testing.T) {
	ctx := context.Background()
	store := &eventWorkflowProgressFailure{MemStore: state.NewMemStore(), fail: true}
	account, err := store.CreateAccount(ctx, "event-workflow@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "event-workflow", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: json.RawMessage(`[
 {"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid","input":{"amount":"{{input.data.amount}}"}}]},
 {"name":"large","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid","filter":{"data":{"amount":{"$gt":1000}}}},"steps":[{"name":"main","path":"/large"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	envelope := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "billing.stripe", Type: "invoice.paid", AccountID: accountID, Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{"amount":150}`)}
	payload, _ := json.Marshal(envelope)
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: &Engine{store: store}}
	if err := loop.routePublishedEventSnapshot(ctx, work); err == nil {
		t.Fatal("disabled runtime should keep workflows pending")
	}
	if len(work.RecipientProgress) != 0 {
		t.Fatal("disabled runtime consumed recipient attempts")
	}
	loop.workflowsDispatched = true
	if err := loop.routePublishedEventSnapshot(ctx, work); err == nil {
		t.Fatal("expected checkpoint interruption")
	}
	if err := loop.routePublishedEventSnapshot(ctx, work); err != nil {
		t.Fatal(err)
	}
	if err := loop.routePublishedEventSnapshot(ctx, work); err != nil {
		t.Fatal(err)
	}
	runs, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("runs=%d err=%v", total, err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	executor := &scheduleStepExecutor{}
	orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, quietLog())
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	completed, err := store.GetWorkflowRun(ctx, runs[0].ID)
	if err != nil || completed.Status != state.WorkflowRunStatusSucceeded || executor.calls != 1 || string(executor.input) != `{"amount":150}` {
		t.Fatalf("run=%+v calls=%d input=%s err=%v", completed, executor.calls, executor.input, err)
	}
	// A target temporarily blocked after acceptance follows the existing
	// bounded routing failure and operator replay lifecycle.
	envelope.ID = uuid.NewString()
	payload, _ = json.Marshal(envelope)
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	maintenance := true
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaintenanceMode: &maintenance, SetMaintenanceMode: true}); err != nil {
		t.Fatal(err)
	}
	for range eventFanoutRecipientMaxAttempts {
		_ = loop.routePublishedEventSnapshot(ctx, blocked)
	}
	if err := store.FinishPublishedEvent(ctx, blocked.ID, blocked.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	failures, err := store.ListEventFanoutFailuresForApp(ctx, app.ID, 20, state.EventFanoutFailureCursor{}, envelope.Source, envelope.ID)
	if err != nil || len(failures) != 1 || !failures[0].Retryable || failures[0].Attempts != eventFanoutRecipientMaxAttempts {
		t.Fatalf("failures=%+v err=%v", failures, err)
	}
	maintenance = false
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaintenanceMode: &maintenance, SetMaintenanceMode: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, app.ID, envelope.Source, envelope.ID, failures[0].SubscriptionID); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.routePublishedEventSnapshot(ctx, replayed); err != nil {
		t.Fatal(err)
	}
	if _, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 2 {
		t.Fatalf("replay runs=%d err=%v", total, err)
	}
}

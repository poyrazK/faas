package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 595; adr: 432 — workflow admission retains its whole-receipt lease
// even when application recipient adoption is enabled on the same scheduler.
func TestEventWorkflowRoutingWithRecipientAdoptionEnabled(t *testing.T) {
	store, ctx := state.NewMemStore(), t.Context()
	account, err := store.CreateAccount(ctx, "workflow-adoption@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workflow-adoption"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
		Workflows: json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	envelope := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "billing.stripe", Type: "invoice.paid",
		AccountID: accountID, Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{}`)}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	loop := (&Loop{engine: &Engine{store: store}, workflowsDispatched: true}).WithEventRecipientClaims(true)
	loop.runEventFanoutSweep(ctx)
	if _, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 1 {
		t.Fatalf("workflow recipient did not start: runs=%d err=%v", total, err)
	}
	if work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC()); err == nil {
		t.Fatalf("workflow recipient was incorrectly adopted: %+v", work)
	}
	loop.runEventFanoutSweep(ctx)
	if _, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 1 {
		t.Fatalf("workflow handoff was duplicated: runs=%d err=%v", total, err)
	}
}

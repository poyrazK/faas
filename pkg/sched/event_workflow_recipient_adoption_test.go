package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 606
// adr: 648 — workflow-only and mixed receipts use recipient ownership.
func TestEventWorkflowRoutingWithRecipientAdoptionEnabled(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store recipientRoutingTestStore = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			for _, independent := range []bool{false, true} {
				for _, mixed := range []bool{false, true} {
					t.Run(fmt.Sprintf("independent=%t/mixed=%t", independent, mixed), func(t *testing.T) {
						testEventWorkflowRoutingWithRecipientAdoptionEnabled(t, store, mixed, independent)
					})
				}
			}
		})
	}
}

// adr: 648 — default adoption includes workflow recipients in mixed receipts.
func testEventWorkflowRoutingWithRecipientAdoptionEnabled(t *testing.T, store recipientRoutingTestStore, mixed, independent bool) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workflow-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending,
		Workflows: json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"billing.*","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/paid"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	var consumer state.App
	if mixed {
		consumer, err = store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "application-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.UpsertEventSubscription(ctx, accountID, consumer.ID, "billing.*", "invoice.paid", nil); err != nil {
			t.Fatal(err)
		}
	}
	envelope := events.Envelope{SpecVersion: "1.0", ID: uuid.NewString(), Source: "billing.stripe", Type: "invoice.paid",
		AccountID: accountID, Time: time.Now().UTC(), DataContentType: "application/json", Data: json.RawMessage(`{}`)}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, &Engine{store: store}, nil).WithWorkflowsDispatched(true).WithEventRecipientClaims(independent)
	loop.runEventFanoutSweep(ctx)
	if _, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 1 {
		t.Fatalf("workflow recipient did not start: runs=%d err=%v", total, err)
	}
	if work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("settled workflow recipient was claimed again: %+v, %v", work, err)
	}
	loop.runEventFanoutSweep(ctx)
	if _, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 1 {
		t.Fatalf("workflow handoff was duplicated: runs=%d err=%v", total, err)
	}
	receipt, err := store.EventReceipt(ctx, accountID, envelope.Source, envelope.ID, state.EventReceiptCursor{}, 100)
	if err != nil || receipt.RecipientClaims != independent || receipt.RoutingSettledAt == nil {
		t.Fatalf("workflow ownership = %+v, %v", receipt, err)
	}
	if mixed {
		invocations, err := store.ListInvocationsForApp(ctx, consumer.ID)
		if err != nil || len(invocations) != 1 {
			t.Fatalf("application handoff = %d, %v", len(invocations), err)
		}
	}
}

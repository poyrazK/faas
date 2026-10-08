package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type workflowRoutingRecoveryStore struct {
	*recipientDualStoreRouteFaultStore
	waitingApp, loseCommitApp string
	lostCommit                bool
}

func (s *workflowRoutingRecoveryStore) AdmitEventWorkflowRecipient(ctx context.Context, claim state.PublishedEventRoutingClaim) (state.EventWorkflowRoutingResult, error) {
	result, err := s.recipientRoutingTestStore.AdmitEventWorkflowRecipient(ctx, claim)
	if err == nil && result.RunCreated && s.claimApps[claim.SubscriptionID] == s.loseCommitApp && !s.lostCommit {
		s.lostCommit = true
		return result, errors.New("workflow admission committed but acknowledgement was lost")
	}
	return result, err
}

func (s *workflowRoutingRecoveryStore) FinishPublishedEventRecipient(ctx context.Context, work *state.PublishedEventRecipientWork, progress state.PublishedEventRecipientProgress, next time.Time) error {
	if work.Recipient.AppID == s.waitingApp && progress.State == state.PublishedEventRecipientPending {
		next = progress.UpdatedAt.Add(2 * time.Hour)
	}
	return s.recipientDualStoreRouteFaultStore.FinishPublishedEventRecipient(ctx, work, progress, next)
}

// adr: 648 — failed workflows and apps recover independently while a workflow
// sibling remains in backoff; restart and an uncertain commit preserve success.
func TestEventWorkflowRecipientRoutingSelectiveRecovery(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store recipientRoutingTestStore = state.NewMemStore()
			restart := func() recipientRoutingTestStore { return store }
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				store = state.NewPgStore(pool)
				restart = func() recipientRoutingTestStore { return state.NewPgStore(pool) }
			}
			testEventWorkflowRecipientRoutingSelectiveRecovery(t, store, restart)
		})
	}
}

func testEventWorkflowRecipientRoutingSelectiveRecovery(t *testing.T, backing recipientRoutingTestStore, restart func() recipientRoutingTestStore) {
	ctx := t.Context()
	store := &workflowRoutingRecoveryStore{recipientDualStoreRouteFaultStore: &recipientDualStoreRouteFaultStore{
		recipientRoutingTestStore: backing, failures: map[string]error{},
	}}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := mustCanonicalEventAccountID(t, account.ID)
	apps := make(map[string]state.App)
	subs := make(map[string]string)
	for _, role := range []string{"healthy_app", "failed_app", "healthy_workflow", "failed_workflow", "waiting_workflow"} {
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: role, Type: state.AppTypeApp, RAMMB: 512})
		if err != nil {
			t.Fatal(err)
		}
		apps[role] = app
		if role == "healthy_app" || role == "failed_app" {
			sub, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
			if err != nil {
				t.Fatal(err)
			}
			subs[role] = sub.ID
			continue
		}
		definitions := json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/paid"}]}]`)
		if role == "healthy_workflow" {
			definitions = json.RawMessage(`[{"name":"paid","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/paid"}]},
			{"name":"filtered","trigger":{"type":"event","source":"orders","event_type":"order.created","filter":{"data":{"amount":{"$gt":1000}}}},"steps":[{"name":"main","path":"/filtered"}]}]`)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:abc", Status: state.DeployPending, Workflows: definitions})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	store.waitingApp, store.loseCommitApp = apps["waiting_workflow"].ID, apps["healthy_workflow"].ID
	store.failures[apps["failed_app"].ID] = errors.New("application admission temporarily unavailable")
	envelope, err := (events.Envelope{ID: "workflow-isolation", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"amount":150}`)}).Normalize(accountID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"failed_workflow", "waiting_workflow"} {
		setWorkflowRecoveryMaintenance(t, store, apps[role], true)
	}
	now := time.Now().UTC().Add(time.Millisecond)
	loop := NewLoop(nil, &Engine{store: store}, nil)
	loop.now = func() time.Time { return now }
	read := func() state.EventReceipt {
		receipt, err := store.EventReceipt(ctx, accountID, envelope.Source, envelope.ID, state.EventReceiptCursor{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	// Adoption is independent of runtime activation. Disabled workflows remain
	// pending with zero claims while ordinary application recipients continue.
	for range 3 {
		loop.runEventFanoutSweep(ctx)
	}
	receipt := read()
	if !receipt.RecipientClaims || receipt.RoutingSummary["enqueued"] != 1 {
		t.Fatalf("disabled workflow receipt=%+v", receipt)
	}
	for _, entry := range receipt.Recipients {
		if entry.WorkflowName == "" {
			continue
		}
		if entry.Routing.Attempts != 0 || entry.Routing.State != "pending" {
			t.Fatalf("disabled workflow consumed attempts=%+v", entry)
		}
		for role, app := range apps {
			if entry.AppID == app.ID && entry.WorkflowName == "paid" {
				subs[role] = entry.SubscriptionID
			}
		}
	}
	loop.WithWorkflowsDispatched(true)
	for range eventFanoutRecipientMaxAttempts {
		loop.runEventFanoutSweep(ctx)
		now = now.Add(301 * time.Second)
	}
	receipt = read()
	if receipt.RoutingSettledAt != nil || receipt.RoutingSummary["failed"] != 2 || receipt.RoutingSummary["pending"] != 1 || !store.lostCommit {
		t.Fatalf("isolated outcomes=%+v lostCommit=%t", receipt, store.lostCommit)
	}
	for _, entry := range receipt.Recipients {
		if entry.SubscriptionID == subs["failed_workflow"] || entry.SubscriptionID == subs["failed_app"] {
			if !entry.RoutingReplayEligible || entry.Routing.Attempts != eventFanoutRecipientMaxAttempts {
				t.Fatalf("failure not independently replayable=%+v", entry)
			}
		}
		if entry.SubscriptionID == subs["waiting_workflow"] && entry.Routing.Attempts != 1 {
			t.Fatalf("sibling backoff ignored=%+v", entry)
		}
	}
	assertWorkflowRecoveryRuns(t, store, apps["healthy_workflow"], 1)
	healthyRuns, _, err := store.ListWorkflowRuns(ctx, apps["healthy_workflow"].ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	healthyRunID := healthyRuns[0].ID
	// Remove future triggers: selective recovery must retain the accepted DAG.
	for _, role := range []string{"failed_workflow", "waiting_workflow"} {
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: apps[role].ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:def", Status: state.DeployPending, Workflows: json.RawMessage(`[]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	setWorkflowRecoveryMaintenance(t, store, apps["failed_workflow"], false)
	delete(store.failures, apps["failed_app"].ID)
	for _, role := range []string{"failed_app", "failed_workflow"} {
		if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, accountID, apps[role].ID, envelope.Source, envelope.ID, subs[role]); err != nil {
			t.Fatalf("selective replay %s: %v", role, err)
		}
	}
	if err := store.DeleteEventSubscription(ctx, subs["failed_app"], accountID, apps["failed_app"].ID); err != nil {
		t.Fatal(err)
	}
	store.recipientRoutingTestStore = restart()
	loop = NewLoop(nil, &Engine{store: store}, nil).WithEventRecipientClaims(false).WithWorkflowsDispatched(true)
	loop.now = func() time.Time { return now }
	loop.runEventFanoutSweep(ctx)
	receipt = read()
	if receipt.RoutingSettledAt != nil || receipt.RoutingSummary["enqueued"] != 4 || receipt.RoutingSummary["pending"] != 1 || receipt.RoutingSummary["filtered"] != 1 {
		t.Fatalf("selective recovery waited for sibling=%+v", receipt)
	}
	for _, entry := range receipt.Recipients {
		if entry.SubscriptionID == subs["waiting_workflow"] && entry.Routing.Attempts != 1 {
			t.Fatalf("selective recovery bypassed sibling backoff=%+v", entry)
		}
		if entry.SubscriptionID == subs["failed_workflow"] {
			if entry.Routing.Generation == nil || *entry.Routing.Generation != 2 || entry.Routing.Attempts != eventFanoutRecipientMaxAttempts+1 || entry.WorkflowRunID == "" || entry.Routing.ReplayCount != 1 {
				t.Fatalf("workflow replay evidence=%+v", entry)
			}
		}
	}
	assertWorkflowRecoveryRuns(t, store, apps["failed_workflow"], 1)
	assertWorkflowRecoveryRuns(t, store, apps["waiting_workflow"], 0)
	setWorkflowRecoveryMaintenance(t, store, apps["waiting_workflow"], false)
	now = now.Add(2 * time.Hour)
	loop.runEventFanoutSweep(ctx)
	if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, payload); err != nil {
		t.Fatal(err)
	}
	loop.runEventFanoutSweep(ctx)
	receipt = read()
	if receipt.RoutingSettledAt == nil || receipt.RoutingSummary["enqueued"] != 5 || receipt.RoutingSummary["filtered"] != 1 {
		t.Fatalf("final receipt=%+v", receipt)
	}
	for _, role := range []string{"healthy_workflow", "failed_workflow", "waiting_workflow"} {
		assertWorkflowRecoveryRuns(t, store, apps[role], 1)
	}
	for _, role := range []string{"healthy_app", "failed_app"} {
		invocations, err := store.ListInvocationsForApp(ctx, apps[role].ID)
		if err != nil || len(invocations) != 1 {
			t.Fatalf("%s invocations=%+v err=%v", role, invocations, err)
		}
	}
	healthyRuns, _, err = store.ListWorkflowRuns(ctx, apps["healthy_workflow"].ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || healthyRuns[0].ID != healthyRunID {
		t.Fatalf("healthy workflow repeated=%+v err=%v", healthyRuns, err)
	}
	history, err := store.ListEventFanoutAttemptsForApp(ctx, apps["healthy_workflow"].ID, 100, state.EventFanoutAttemptCursor{}, envelope.Source, envelope.ID, subs["healthy_workflow"])
	if err != nil || len(history) != 1 || history[0].State != "enqueued" {
		t.Fatalf("lost acknowledgement overwrote history=%+v err=%v", history, err)
	}
}

func setWorkflowRecoveryMaintenance(t *testing.T, store state.Store, app state.App, value bool) {
	t.Helper()
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{MaintenanceMode: &value, SetMaintenanceMode: true}); err != nil {
		t.Fatal(err)
	}
}

func assertWorkflowRecoveryRuns(t *testing.T, store state.Store, app state.App, want int) {
	t.Helper()
	if _, total, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != want {
		t.Fatalf("%s runs=%d want=%d err=%v", app.Slug, total, want, err)
	}
	if rows, err := store.ListInvocationsForApp(t.Context(), app.ID); err != nil || len(rows) != 0 {
		t.Fatalf("workflow routed as app=%+v err=%v", rows, err)
	}
}

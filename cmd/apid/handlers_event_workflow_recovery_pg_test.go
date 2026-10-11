//go:build !no_pg

package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 648 — public recovery actions compose with independent workflow
// admission. Routing failures/backoff are fixtures; scheduler acceptance drives
// the real failure budget, disabled-runtime behavior and restart separately.
func TestEventWorkflowRecipientRecoveryPostgresHTTP(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	store := e.store.(*state.PgStore)
	ctx := t.Context()
	workflow, err := store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "workflow-recovery", Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: workflow.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployPending, Workflows: json.RawMessage(`[
		{"name":"failed","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/paid"}]},
		{"name":"waiting","trigger":{"type":"event","source":"orders","event_type":"order.created"},"steps":[{"name":"main","path":"/waiting"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	healthy, err := store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "healthy-recovery", Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, e.acct.ID, healthy.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}
	publish := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{ID: "workflow-recovery", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"amount":150}`)}, nil)
	if publish.Code != http.StatusAccepted {
		t.Fatalf("publish=%d %s", publish.Code, publish.Body)
	}
	parent, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitializePublishedEventRecipients(ctx, parent, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var failedID, waitingID string
	for range 3 {
		work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(work.Recipient.Workflow) == 0 {
			_, err := store.AdmitPublishedEventRecipient(ctx, state.PublishedEventRoutingClaim{OutboxID: work.OutboxID,
				SubscriptionID: work.Recipient.ID, ClaimToken: work.ClaimToken, Generation: work.Generation})
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var definition api.WorkflowSpec
		if err := json.Unmarshal(work.Recipient.Workflow, &definition); err != nil {
			t.Fatal(err)
		}
		progress := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientPending, Attempts: work.TotalAttempts, UpdatedAt: time.Now().UTC(), LastError: "routing unavailable"}
		if definition.Name == "failed" {
			failedID = work.Recipient.ID
			progress.State, progress.FailureCode, progress.Retryable = state.PublishedEventRecipientFailed, state.EventFanoutFailureCodeInvocationEnqueueFailed, true
		} else {
			waitingID = work.Recipient.ID
		}
		if err := store.FinishPublishedEventRecipient(ctx, work, progress, time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	read := func() api.EventReceiptResponse {
		r := e.do(t, http.MethodGet, eventReceiptURL("orders", "workflow-recovery"), nil, nil)
		var receipt api.EventReceiptResponse
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &receipt) != nil {
			t.Fatalf("receipt=%d %s", r.Code, r.Body)
		}
		return receipt
	}
	receipt := read()
	if receipt.RoutingMode != "recipient" || receipt.RoutingSettledAt != nil || receipt.RoutingSummary["failed"] != 1 || receipt.RoutingSummary["pending"] != 1 {
		t.Fatalf("initial receipt=%+v", receipt)
	}
	selected := eventWorkflowRecoveryRecipient(t, receipt, failedID)
	if selected.WorkflowName != "failed" || len(selected.RecoveryActions) != 1 || selected.RecoveryActions[0].Kind != "routing_replay" {
		t.Fatalf("workflow recovery action=%+v", selected)
	}
	action := selected.RecoveryActions[0]
	replay := e.do(t, action.Method, action.URL, action.Body, nil)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("selective replay=%d %s", replay.Code, replay.Body)
	}
	// A reconstructed store resumes the new generation from its captured DAG.
	store = state.NewPgStore(e.pool)
	work, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
	if err != nil || work.Recipient.ID != failedID || work.Generation != 2 {
		t.Fatalf("replay claim=%+v err=%v", work, err)
	}
	result, err := store.AdmitEventWorkflowRecipient(ctx, state.PublishedEventRoutingClaim{OutboxID: work.OutboxID,
		SubscriptionID: work.Recipient.ID, ClaimToken: work.ClaimToken, Generation: work.Generation})
	if err != nil || !result.RunCreated || result.ReceiptSettled {
		t.Fatalf("workflow admission=%+v err=%v", result, err)
	}
	receipt = read()
	selected = eventWorkflowRecoveryRecipient(t, receipt, failedID)
	waiting := eventWorkflowRecoveryRecipient(t, receipt, waitingID)
	if selected.Routing.State != "enqueued" || selected.WorkflowRunID != result.RunID || selected.WorkflowRunStatus != "pending" || selected.Routing.ReplayCount != 1 || len(selected.RecoveryActions) != 0 ||
		waiting.Routing.State != "pending" || waiting.Routing.Attempts != 1 || receipt.RoutingSettledAt != nil {
		t.Fatalf("recovered=%+v waiting=%+v", selected, waiting)
	}
	if _, total, err := store.ListWorkflowRuns(ctx, workflow.ID, state.ListWorkflowRunsOpts{Limit: 10}); err != nil || total != 1 {
		t.Fatalf("workflow runs=%d err=%v", total, err)
	}
	if invocations, err := store.ListInvocationsForApp(ctx, healthy.ID); err != nil || len(invocations) != 1 {
		t.Fatalf("healthy sibling repeated=%+v err=%v", invocations, err)
	}
	for _, link := range []string{selected.AttemptHistoryURL, selected.FanoutHistoryURL} {
		if r := e.do(t, http.MethodGet, link, nil, nil); r.Code != http.StatusOK {
			t.Fatalf("history=%s status=%d body=%s", link, r.Code, r.Body)
		}
	}
	replay = e.do(t, action.Method, action.URL, action.Body, nil)
	if replay.Code != http.StatusNotFound {
		t.Fatalf("settled workflow replayed again=%d %s", replay.Code, replay.Body)
	}
}

func eventWorkflowRecoveryRecipient(t *testing.T, receipt api.EventReceiptResponse, id string) api.EventReceiptRecipientResponse {
	t.Helper()
	for _, entry := range receipt.Recipients {
		if entry.SubscriptionID == id {
			return entry
		}
	}
	t.Fatalf("recipient %s is missing from %+v", id, receipt)
	return api.EventReceiptRecipientResponse{}
}

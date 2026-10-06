package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 619
func TestEventReceiptPublishLinkAndStableAcceptance(t *testing.T) {
	e := setup(t, api.PlanPro)
	request := api.PublishEventRequest{ID: "evt/?+&", Source: "https://orders.example/a?b=1&c=2", Type: "created", Data: json.RawMessage(`{}`)}
	var first api.PublishEventResponse
	for attempt := range 2 {
		rec := e.do(t, "POST", "/v1/events:publish", request, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("publish: %d %s", rec.Code, rec.Body)
		}
		var published api.PublishEventResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &published); err != nil {
			t.Fatal(err)
		}
		if rec.Header().Get("Location") != published.ReceiptURL {
			t.Fatalf("location: %+v", published)
		}
		if attempt == 0 {
			first = published
		} else if !published.AcceptedAt.Equal(first.AcceptedAt) || published.ReceiptURL != first.ReceiptURL {
			t.Fatalf("retry changed acceptance: %+v %+v", first, published)
		}
	}
	rec := e.do(t, "GET", first.ReceiptURL, nil, nil)
	var receipt api.EventReceiptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || receipt.EventID != request.ID || receipt.EventSource != request.Source || !receipt.AcceptedAt.Equal(first.AcceptedAt) || !receipt.SnapshotCaptured || receipt.RecipientCount != 0 || receipt.Recipients == nil || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
	}
}

func seedAPIReceipt(t *testing.T, e testEnv) *state.PublishedEventWork {
	t.Helper()
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: e.acct.ID, Slug: "receipt-app", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range [][2]string{{"orders", "created"}, {"orders", "*"}, {"*", "created"}} {
		if _, _, err := e.store.UpsertEventSubscription(ctx, e.acct.ID, app.ID, pattern[0], pattern[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.do(t, "POST", "/v1/events:publish", api.PublishEventRequest{ID: "receipt-1", Source: "orders", Type: "created", Data: json.RawMessage(`{}`)}, nil)
	if rec.Code != 202 {
		t.Fatalf("publish: %s", rec.Body)
	}
	work, err := e.store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return work
}

// adr: 619
func TestEventReceiptPaginationIsolationAndRecovery(t *testing.T) {
	e := setup(t, api.PlanPro)
	work := seedAPIReceipt(t, e)
	ctx := context.Background()
	// The first two routes enqueue an original execution; the third route fails.
	for range 3 {
		claim, err := e.store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		status := state.PublishedEventRecipientFailed
		for i, recipient := range work.RecipientSnapshot[:2] {
			if claim.Recipient.ID == recipient.ID {
				status = state.PublishedEventRecipientEnqueued
				executionState := state.InvocationCompleted
				if i == 1 {
					executionState = state.InvocationDeadLetter
				}
				id := state.PublishedEventInvocationID(e.acct.ID, "orders", "receipt-1", recipient.ID)
				if _, err := e.store.EnqueueInvocation(ctx, state.Invocation{ID: id, AppID: recipient.AppID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke, State: executionState, DueAt: time.Now(), Attempts: 2}); err != nil {
					t.Fatal(err)
				}
			}
		}
		progress := state.PublishedEventRecipientProgress{State: status, Attempts: claim.TotalAttempts, UpdatedAt: time.Now(), LastError: "route outage", Retryable: true, FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed}
		if err := e.store.FinishPublishedEventRecipient(ctx, claim, progress, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	path := eventReceiptURL("orders", "receipt-1")
	firstRec := e.do(t, "GET", path+"&limit=1", nil, nil)
	var first api.EventReceiptResponse
	if err := json.Unmarshal(firstRec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if firstRec.Code != 200 || first.RecipientCount != 3 || first.NextAfter == "" || first.RoutingSummary["enqueued"] != 2 || first.RoutingSummary["failed"] != 1 || len(first.Recipients) != 1 || first.Recipients[0].Execution.State != "completed" || len(first.Recipients[0].RecoveryActions) != 0 {
		t.Fatalf("first receipt: %s", firstRec.Body)
	}
	secondRec := e.do(t, "GET", path+"&after="+url.QueryEscape(first.NextAfter), nil, nil)
	var second api.EventReceiptResponse
	if err := json.Unmarshal(secondRec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if secondRec.Code != 200 || len(second.Recipients) != 2 || second.NextAfter != "" {
		t.Fatalf("second receipt: %s", secondRec.Body)
	}
	dead, failed := second.Recipients[0], second.Recipients[1]
	deadLetters, err := e.store.ListDeadLetterEvents(ctx, work.RecipientSnapshot[1].AppID, 10, "")
	if err != nil || len(deadLetters) != 1 || deadLetters[0].SourceID != dead.Execution.InvocationID {
		t.Fatalf("dead-letter projection: %+v %v", deadLetters, err)
	}
	deadLetterURL := "/v1/apps/receipt-app/dlq/" + deadLetters[0].ID + "/replay"
	if len(dead.RecoveryActions) != 1 || dead.RecoveryActions[0].Kind != "dead_letter_replay" || dead.RecoveryActions[0].URL != deadLetterURL {
		t.Fatalf("dead letter action: %+v", dead)
	}
	if len(failed.RecoveryActions) != 1 || failed.RecoveryActions[0].Kind != "routing_replay" || failed.RecoveryActions[0].Body.SubscriptionID != failed.SubscriptionID || failed.RecoveryActions[0].Body.EventSource != "orders" || failed.FanoutHistoryURL == "" {
		t.Fatalf("route action: %+v", failed)
	}
	// Returned selective replay acts on only this failed recipient.
	action := failed.RecoveryActions[0]
	replay := e.do(t, action.Method, action.URL, action.Body, nil)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}

	dlqAction := dead.RecoveryActions[0]
	dlqReplay := e.do(t, dlqAction.Method, dlqAction.URL, nil, nil)
	if dlqReplay.Code != http.StatusAccepted {
		t.Fatalf("dead-letter replay: %d %s", dlqReplay.Code, dlqReplay.Body)
	}
	updatedRec := e.do(t, "GET", path, nil, nil)
	var updated api.EventReceiptResponse
	if err := json.Unmarshal(updatedRec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updatedRec.Code != 200 || updated.Recipients[1].Execution.InvocationID != dead.Execution.InvocationID || updated.Recipients[1].Execution.State != "pending" || updated.Recipients[1].Execution.ReplayGeneration != 1 || updated.Recipients[1].Execution.Attempts != 0 || updated.Recipients[2].Routing.ReplayCount != 1 {
		t.Fatalf("recovery evidence: %d %s", updatedRec.Code, updatedRec.Body)
	}
	other, err := e.store.CreateAccount(ctx, "other-receipt@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(ctx, other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "GET", path, nil, map[string]string{"Authorization": "Bearer " + token}); rec.Code != 404 {
		t.Fatalf("cross-account: %d %s", rec.Code, rec.Body)
	}
	for _, invalid := range []string{eventReceiptURL("other", "receipt-1") + "&after=" + url.QueryEscape(first.NextAfter), path + "&after=bad", path + "&limit=201", "/v1/events/receipt?id=receipt-1", "/v1/events/receipt?source=" + strings.Repeat("a", 257) + "&id=receipt-1"} {
		if rec := e.do(t, "GET", invalid, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid %s: %d %s", invalid, rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, "GET", eventReceiptURL("unknown", "receipt-1"), nil, nil); rec.Code != 404 {
		t.Fatalf("missing source: %d", rec.Code)
	}
}

// adr: 619
func TestEventReceiptReadScope(t *testing.T) {
	for _, test := range []struct {
		scope  string
		status int
	}{{api.ScopeAppsRead, 404}, {api.ScopeEventsPublish, 403}, {api.ScopeUsageRead, 403}} {
		t.Run(test.scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{test.scope})
			rec := e.do(t, "GET", eventReceiptURL("orders", "unknown"), nil, nil)
			if rec.Code != test.status {
				t.Fatalf("scope: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

// adr: 619
func TestEventReceiptCursorSupportsMaximumEncodedIdentity(t *testing.T) {
	receipt := state.EventReceipt{OutboxID: 1, NextPosition: 1, EventSource: strings.Repeat("&", 256), EventID: strings.Repeat("<", 256)}
	cursor := encodeEventReceiptCursor("00000000-0000-0000-0000-000000000001", receipt)
	decoded, err := decodeEventReceiptCursor(cursor, "00000000-0000-0000-0000-000000000001", receipt.EventSource, receipt.EventID)
	if err != nil || decoded.OutboxID != 1 || decoded.Position != 1 {
		t.Fatalf("accepted identity produced unusable cursor: %d %v", len(cursor), err)
	}
}

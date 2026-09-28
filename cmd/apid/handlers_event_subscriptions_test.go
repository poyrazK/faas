package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListEventDeliveries_ReturnsOnlyEventInvocations(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "delivery-app")
	now := time.Now().UTC()
	for _, inv := range []state.Invocation{
		{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			State: state.InvocationCompleted, Headers: json.RawMessage(`{"x-gregale-event-id":"evt-1","x-gregale-event-source":"billing","x-gregale-event-type":"invoice.paid","x-gregale-event-subscription-id":"sub-1"}`),
			DueAt: now, CreatedAt: now, Attempts: 1,
		},
		{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			State: state.InvocationFailed, Headers: json.RawMessage(`{"x-gregale-event-id":"evt-2","x-gregale-event-source":"billing","x-gregale-event-type":"invoice.failed"}`),
			DueAt: now.Add(-time.Second), CreatedAt: now.Add(-time.Second), Attempts: 3, LastError: "worker unavailable",
		},
		{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			State: state.InvocationCompleted, Headers: json.RawMessage(`{"x-user":"ordinary"}`),
			DueAt: now.Add(-2 * time.Second), CreatedAt: now.Add(-2 * time.Second),
		},
	} {
		if _, err := e.store.EnqueueInvocation(context.Background(), inv); err != nil {
			t.Fatalf("enqueue invocation: %v", err)
		}
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?state=failed", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.EventDeliveryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Deliveries) != 1 || out.Deliveries[0].EventID != "evt-2" || out.Deliveries[0].Attempts != 3 {
		t.Fatalf("deliveries = %+v, want filtered failed event", out.Deliveries)
	}
	if out.Deliveries[0].LastError != "worker unavailable" {
		t.Fatalf("last_error = %q", out.Deliveries[0].LastError)
	}

	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("event filter status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode event filter: %v", err)
	}
	if len(out.Deliveries) != 1 || out.Deliveries[0].SubscriptionID != "sub-1" {
		t.Fatalf("event filter deliveries = %+v", out.Deliveries)
	}

	// A cursor is positioned in the unfiltered event stream. Changing the
	// filter must not restart pagination when the cursor row is excluded.
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?limit=2", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Deliveries) != 2 {
		t.Fatalf("decode page: err=%v deliveries=%+v", err, out.Deliveries)
	}
	if out.Deliveries[1].EventID != "evt-2" {
		t.Fatalf("cursor event = %q, want evt-2", out.Deliveries[1].EventID)
	}
	before := out.Deliveries[1].InvocationID
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&before="+before, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered cursor status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Deliveries) != 0 {
		t.Fatalf("filtered cursor: err=%v deliveries=%+v, want empty page", err, out.Deliveries)
	}
}

func TestListEventDeliveries_CrossAccountIsNotVisible(t *testing.T) {
	owner := setup(t, api.PlanPro)
	appID := mustSeedApp(t, owner, "private-deliveries")
	if _, err := owner.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: appID, AccountID: owner.acct.ID, Source: state.InvocationAsyncInvoke,
		Headers: json.RawMessage(`{"x-gregale-event-id":"evt-private"}`), DueAt: time.Now(), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue invocation: %v", err)
	}
	foreignAcct, err := owner.store.CreateAccount(context.Background(), "foreign-deliveries@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	foreignToken, foreignHash, _ := api.GenerateAPIKey()
	if _, err := owner.store.CreateAPIKey(context.Background(), foreignAcct.ID, foreignHash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatalf("create foreign key: %v", err)
	}
	foreign := owner
	foreign.acct = foreignAcct
	foreign.key = foreignToken
	rec := foreign.do(t, http.MethodGet, "/v1/apps/private-deliveries/event-deliveries", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReplayEventFanoutFailure_RequeuesOneRecipient(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "replay-event-app")
	subscription, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, appID, "orders", "order.created", nil)
	if err != nil {
		t.Fatalf("seed event subscription: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "apid", "event.published", &e.acct.ID,
		json.RawMessage(`{"id":"evt-replay-api","source":"orders","type":"order.created","data":{"amount":1}}`)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	work, err := e.store.ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event: %v", err)
	}
	if err := e.store.RecordPublishedEventRecipientProgress(context.Background(), work.ID, work.ClaimToken, subscription.ID,
		state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 1,
			FailureCode: state.EventFanoutFailureCodeTargetUnavailable,
			LastError:   "target unavailable", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record failed recipient: %v", err)
	}
	if err := e.store.FinishPublishedEvent(context.Background(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish event: %v", err)
	}
	list := e.do(t, http.MethodGet, "/v1/apps/replay-event-app/event-deliveries?event_id=evt-replay-api", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body.String())
	}
	var listed api.EventDeliveryListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode event deliveries: %v", err)
	}
	if len(listed.FanoutFailures) != 1 || listed.FanoutFailures[0].EventID != "evt-replay-api" ||
		listed.FanoutFailures[0].FailureCode != state.EventFanoutFailureCodeTargetUnavailable || listed.FanoutFailures[0].Retryable {
		t.Fatalf("fanout failures = %+v, want target_unavailable and retryable=false", listed.FanoutFailures)
	}

	rec := e.do(t, http.MethodPost, "/v1/apps/replay-event-app/event-deliveries:replay-fanout-failure", api.ReplayEventFanoutFailureRequest{
		EventID: "evt-replay-api", EventSource: "orders", SubscriptionID: subscription.ID,
	}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.ReplayEventFanoutFailureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if response.EventID != "evt-replay-api" || response.EventSource != "orders" ||
		response.SubscriptionID != subscription.ID || response.State != state.PublishedEventRecipientPending {
		t.Fatalf("replay response = %+v", response)
	}
}

func TestReplayRetryableEventFanoutFailures_RequeuesBoundedRetryableRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "replay-retryable-event-app")
	retryable, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, appID, "orders.us", "order.created", nil)
	if err != nil {
		t.Fatalf("seed retryable subscription: %v", err)
	}
	permanent, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, appID, "orders.*", "order.created", nil)
	if err != nil {
		t.Fatalf("seed permanent subscription: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "apid", "event.published", &e.acct.ID,
		json.RawMessage(`{"id":"evt-replay-retryable","source":"orders.us","type":"order.created","data":{}}`)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	work, err := e.store.ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event: %v", err)
	}
	now := time.Now().UTC()
	for _, outcome := range []struct {
		subscription state.EventSubscription
		code         string
		retryable    bool
	}{
		{retryable, state.EventFanoutFailureCodeInvocationEnqueueFailed, true},
		{permanent, state.EventFanoutFailureCodeTargetUnavailable, false},
	} {
		if err := e.store.RecordPublishedEventRecipientProgress(context.Background(), work.ID, work.ClaimToken, outcome.subscription.ID,
			state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 12,
				FailureCode: outcome.code, Retryable: outcome.retryable, LastError: "route failed", UpdatedAt: now}); err != nil {
			t.Fatalf("record failed recipient %s (snapshot=%+v): %v", outcome.subscription.ID, work.RecipientSnapshot, err)
		}
	}
	if err := e.store.FinishPublishedEvent(context.Background(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish event: %v", err)
	}

	rec := e.do(t, http.MethodPost, "/v1/apps/replay-retryable-event-app/event-deliveries:replay-retryable-fanout-failures",
		api.ReplayRetryableEventFanoutFailuresRequest{EventSource: "orders.us", EventID: "evt-replay-retryable", Limit: 1}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.ReplayRetryableEventFanoutFailuresResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.AppSlug != "replay-retryable-event-app" || response.ReplayedCount != 1 || response.HasMore {
		t.Fatalf("response = %+v, want one retryable row and no more", response)
	}
	replayed, err := e.store.ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("claim replay: %v", err)
	}
	if got := replayed.RecipientProgress[retryable.ID]; got.State != state.PublishedEventRecipientPending || got.FailureCode != "" || got.Retryable {
		t.Fatalf("retryable progress = %+v, want cleared pending", got)
	}
	if got := replayed.RecipientProgress[permanent.ID]; got.State != state.PublishedEventRecipientFailed || got.Retryable {
		t.Fatalf("permanent progress = %+v, want unchanged failed", got)
	}
}

func TestReplayRetryableEventFanoutFailuresRejectsInvalidLimit(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "replay-retryable-invalid-limit")
	rec := e.do(t, http.MethodPost, "/v1/apps/replay-retryable-invalid-limit/event-deliveries:replay-retryable-fanout-failures",
		api.ReplayRetryableEventFanoutFailuresRequest{Limit: state.EventFanoutReplayBatchMax + 1}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReplayRetryableEventFanoutFailuresRequiresCompleteEventIdentity(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "replay-retryable-incomplete-identity")
	rec := e.do(t, http.MethodPost, "/v1/apps/replay-retryable-incomplete-identity/event-deliveries:replay-retryable-fanout-failures",
		api.ReplayRetryableEventFanoutFailuresRequest{EventID: "evt-only"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListEventSubscriptions_ReturnsReconciledManifestRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "events-app")
	_, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, appID,
		"orders", "order.created", json.RawMessage(`{"status":"paid"}`))
	if err != nil {
		t.Fatalf("seed event subscription: %v", err)
	}

	rec := e.do(t, "GET", "/v1/apps/events-app/event-subscriptions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.EventSubscriptionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AppSlug != "events-app" {
		t.Fatalf("app_slug = %q, want events-app", out.AppSlug)
	}
	if len(out.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(out.Subscriptions))
	}
	got := out.Subscriptions[0]
	if got.Source != "orders" || got.Type != "order.created" || !got.Enabled {
		t.Fatalf("subscription = %+v", got)
	}
	if string(got.Filter) != `{"status":"paid"}` {
		t.Fatalf("filter = %s, want normalized object", got.Filter)
	}
}

func TestListEventSubscriptions_CrossAccountIsNotVisible(t *testing.T) {
	first := setup(t, api.PlanPro)
	appID := mustSeedApp(t, first, "private-events-app")
	if _, _, err := first.store.UpsertEventSubscription(context.Background(), first.acct.ID, appID,
		"orders", "order.created", nil); err != nil {
		t.Fatalf("seed event subscription: %v", err)
	}

	secondAcct, err := first.store.CreateAccount(context.Background(), "other-events@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create second account: %v", err)
	}
	secondToken, secondHash, _ := api.GenerateAPIKey()
	if _, err := first.store.CreateAPIKey(context.Background(), secondAcct.ID, secondHash, "second", api.ScopesAdminOnly); err != nil {
		t.Fatalf("create second key: %v", err)
	}
	second := first
	second.acct = secondAcct
	second.key = secondToken

	rec := second.do(t, "GET", "/v1/apps/private-events-app/event-subscriptions", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

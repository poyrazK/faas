package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
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
			State: state.InvocationCompleted, Headers: json.RawMessage(`{"x-gregale-event-id":"evt-1","x-gregale-event-source":"shipping","x-gregale-event-type":"shipment.sent","x-gregale-event-subscription-id":"sub-2"}`),
			DueAt: now.Add(time.Second), CreatedAt: now.Add(time.Second), Attempts: 1,
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
	if out.Deliveries[0].InvocationSource != string(state.InvocationAsyncInvoke) {
		t.Fatalf("invocation_source = %q, want async_invoke", out.Deliveries[0].InvocationSource)
	}

	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("event filter status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode event filter: %v", err)
	}
	if len(out.Deliveries) != 2 {
		t.Fatalf("event filter deliveries = %+v", out.Deliveries)
	}
	gotSources := map[string]bool{}
	for _, delivery := range out.Deliveries {
		gotSources[delivery.EventSource] = true
	}
	if !gotSources["billing"] || !gotSources["shipping"] {
		t.Fatalf("event ID filter sources = %v, want billing and shipping", gotSources)
	}
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "billing", source: "billing", want: "sub-1"},
		{name: "shipping", source: "shipping", want: "sub-2"},
	} {
		t.Run("source_filter_"+tc.name, func(t *testing.T) {
			path := "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&event_source=" + tc.source
			rec := e.do(t, http.MethodGet, path, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("source filter status = %d; body=%s", rec.Code, rec.Body.String())
			}
			var filtered api.EventDeliveryListResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil {
				t.Fatalf("decode source filter: %v", err)
			}
			if len(filtered.Deliveries) != 1 || filtered.Deliveries[0].EventSource != tc.source || filtered.Deliveries[0].SubscriptionID != tc.want {
				t.Fatalf("source filter deliveries = %+v", filtered.Deliveries)
			}
		})
	}

	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_source=billing", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("source without ID status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// The generated cursor is bound to the exact event identity and state.
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&event_source=shipping&limit=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Deliveries) != 1 {
		t.Fatalf("decode page: err=%v deliveries=%+v", err, out.Deliveries)
	}
	if out.NextBefore == "" {
		t.Fatal("full page has empty next_before")
	}
	before := out.NextBefore
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&event_source=shipping&before="+before, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching filtered cursor status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Deliveries) != 0 {
		t.Fatalf("matching filtered cursor: err=%v deliveries=%+v, want empty page", err, out.Deliveries)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&event_source=billing&before="+before, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched source cursor status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-2&event_source=shipping&before="+before, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched event ID cursor status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&event_source=shipping&state=failed&before="+before, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched state cursor status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&before="+before, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("source-bound cursor without source status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// The legacy raw invocation-ID cursor remains accepted for clients that
	// continue event-ID-only inspection.
	legacyRec := e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&limit=2", nil, nil)
	if legacyRec.Code != http.StatusOK {
		t.Fatalf("legacy cursor seed status = %d; body=%s", legacyRec.Code, legacyRec.Body.String())
	}
	var legacyPage api.EventDeliveryListResponse
	if err := json.Unmarshal(legacyRec.Body.Bytes(), &legacyPage); err != nil || len(legacyPage.Deliveries) != 2 {
		t.Fatalf("decode legacy cursor seed: err=%v deliveries=%+v", err, legacyPage.Deliveries)
	}
	legacyRawCursor := legacyPage.Deliveries[0].InvocationID
	legacyRec = e.do(t, http.MethodGet, "/v1/apps/delivery-app/event-deliveries?event_id=evt-1&before="+legacyRawCursor, nil, nil)
	if legacyRec.Code != http.StatusOK {
		t.Fatalf("legacy raw cursor status = %d, want 200; body=%s", legacyRec.Code, legacyRec.Body.String())
	}
}

func TestListEventDeliveries_IncludesEventReplaysButExcludesOrdinaryReplays(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "delivery-replay-app")
	ctx := context.Background()
	seedFailed := func(headers json.RawMessage) string {
		t.Helper()
		inv, err := e.store.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/", Headers: headers,
			Payload: json.RawMessage(`{"amount":1}`), DueAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("enqueue invocation: %v", err)
		}
		if _, err := e.store.ClaimInvocation(ctx, inv.ID, "delivery-replay-test", 30); err != nil {
			t.Fatalf("claim invocation %s: %v", inv.ID, err)
		}
		if err := e.store.FailInvocation(ctx, inv.ID, "worker unavailable", 0, 0); err != nil {
			t.Fatalf("fail invocation %s: %v", inv.ID, err)
		}
		return inv.ID
	}
	eventInvocationID := seedFailed(json.RawMessage(`{"x-gregale-event-id":"evt-replay-visible","x-gregale-event-source":"orders.us","x-gregale-event-type":"order.created","x-gregale-event-subscription-id":"sub-replay"}`))
	ordinaryInvocationID := seedFailed(json.RawMessage(`{"x-user":"ordinary"}`))

	var eventReplay api.AsyncInvokeResponse
	rec := e.do(t, http.MethodPost, "/v1/invocations/"+eventInvocationID+"/replay", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("event replay status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &eventReplay); err != nil {
		t.Fatalf("decode event replay response: %v", err)
	}
	var ordinaryReplay api.AsyncInvokeResponse
	rec = e.do(t, http.MethodPost, "/v1/invocations/"+ordinaryInvocationID+"/replay", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ordinary replay status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ordinaryReplay); err != nil {
		t.Fatalf("decode ordinary replay response: %v", err)
	}

	path := "/v1/apps/delivery-replay-app/event-deliveries?event_source=orders.us&event_id=evt-replay-visible"
	rec = e.do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("event delivery list status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var listed api.EventDeliveryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode event delivery list: %v", err)
	}
	if len(listed.Deliveries) != 2 {
		t.Fatalf("event deliveries = %+v, want original plus replay", listed.Deliveries)
	}
	byID := make(map[string]api.EventDeliveryResponse, len(listed.Deliveries))
	for _, delivery := range listed.Deliveries {
		byID[delivery.InvocationID] = delivery
		if delivery.EventID != "evt-replay-visible" || delivery.EventSource != "orders.us" {
			t.Errorf("delivery identity = %s/%s, want orders.us/evt-replay-visible", delivery.EventSource, delivery.EventID)
		}
	}
	if got := byID[eventInvocationID].InvocationSource; got != string(state.InvocationAsyncInvoke) {
		t.Errorf("original invocation source = %q, want async_invoke", got)
	}
	if got := byID[eventReplay.ID].InvocationSource; got != string(state.InvocationReplay) {
		t.Errorf("replayed invocation source = %q, want replay", got)
	}
	if _, included := byID[ordinaryReplay.ID]; included {
		t.Errorf("ordinary replay %s appeared in event delivery history", ordinaryReplay.ID)
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

func TestListEventDeliveries_FiltersFanoutFailuresBySourceAndBindsCursor(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "delivery-fanout-filter")
	ctx := context.Background()
	for _, spec := range []struct {
		source string
		typ    string
	}{
		{source: "orders.us", typ: "order.created"},
		{source: "orders.*", typ: "order.created"},
		{source: "orders.eu", typ: "invoice.created"},
	} {
		if _, _, err := e.store.UpsertEventSubscription(ctx, e.acct.ID, appID, spec.source, spec.typ, nil); err != nil {
			t.Fatalf("seed subscription %s/%s: %v", spec.source, spec.typ, err)
		}
	}

	for _, event := range []struct {
		source string
		typ    string
		want   int
	}{
		{source: "orders.us", typ: "order.created", want: 2},
		{source: "orders.eu", typ: "invoice.created", want: 1},
	} {
		payload := json.RawMessage(`{"id":"evt-duplicate","source":"` + event.source + `","type":"` + event.typ + `","data":{}}`)
		if err := e.store.AppendEvent(ctx, "apid", "event.published", &e.acct.ID, payload); err != nil {
			t.Fatalf("append %s event: %v", event.source, err)
		}
		work, err := e.store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatalf("claim %s event: %v", event.source, err)
		}
		if len(work.RecipientSnapshot) != event.want {
			t.Fatalf("%s recipient snapshot has %d entries, want %d: %+v", event.source, len(work.RecipientSnapshot), event.want, work.RecipientSnapshot)
		}
		for _, recipient := range work.RecipientSnapshot {
			if err := e.store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID,
				state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 1,
					FailureCode: state.EventFanoutFailureCodeTargetUnavailable, LastError: "target unavailable", UpdatedAt: time.Now().UTC()}); err != nil {
				t.Fatalf("record %s failure for %s: %v", event.source, recipient.ID, err)
			}
		}
		if err := e.store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatalf("finish %s event: %v", event.source, err)
		}
	}

	listPath := "/v1/apps/delivery-fanout-filter/event-deliveries?event_source=orders.us&event_id=evt-duplicate&state=failed&limit=1"
	rec := e.do(t, http.MethodGet, listPath, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("source-filtered list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var first api.EventDeliveryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.FanoutFailures) != 1 || first.FanoutFailures[0].EventSource != "orders.us" || first.NextFanoutBefore == "" {
		t.Fatalf("first source-filtered failure page = %+v, want one US failure and a cursor", first)
	}

	page2Path := listPath + "&fanout_before=" + first.NextFanoutBefore
	rec = e.do(t, http.MethodGet, page2Path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-filter fanout cursor status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var second api.EventDeliveryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(second.FanoutFailures) != 1 || second.FanoutFailures[0].EventSource != "orders.us" ||
		second.FanoutFailures[0].SubscriptionID == first.FanoutFailures[0].SubscriptionID {
		t.Fatalf("second source-filtered failure page = %+v, want the other US failure", second.FanoutFailures)
	}

	mismatchedPath := "/v1/apps/delivery-fanout-filter/event-deliveries?event_source=orders.eu&event_id=evt-duplicate&state=failed&limit=1&fanout_before=" + first.NextFanoutBefore
	rec = e.do(t, http.MethodGet, mismatchedPath, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched fanout cursor status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/delivery-fanout-filter/event-deliveries?event_source=orders.us", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("fanout source without event ID status = %d, want 400; body=%s", rec.Code, rec.Body.String())
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
	subscription, _, err := e.store.UpsertEventSubscription(context.Background(), e.acct.ID, appID,
		"orders", "order.created", json.RawMessage(`{"status":"paid"}`))
	if err != nil {
		t.Fatalf("seed event subscription: %v", err)
	}
	policy := workpolicy.Policy{Name: "ordered-orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
	if _, err := e.store.UpsertAppWorkPolicy(context.Background(), e.acct.ID, appID, policy); err != nil {
		t.Fatalf("seed event work policy: %v", err)
	}
	if _, err := e.store.SetEventWorkBinding(context.Background(), appID, subscription.ID, policy.Name,
		"data.order_id", state.EventWorkBindingOptions{Ordered: true}); err != nil {
		t.Fatalf("seed ordered event work binding: %v", err)
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
	if got.Source != "orders" || got.Type != "order.created" || !got.Enabled || !got.Ordered {
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

package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListEventFanoutAttemptHistoryRequiresIdentityAndPagesReplayTimeline(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "fanout-history-api")
	subscription, _, err := (*e.Store()).UpsertEventSubscription(context.Background(), (*e.Acct()).ID, appID, "orders", "order.created", nil)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	eventID := "evt-history-api"
	if err := (*e.Store()).AppendEvent(context.Background(), "apid", "event.published", &(*e.Acct()).ID,
		json.RawMessage(`{"id":"evt-history-api","source":"orders","type":"order.created","data":{}}`)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	work, err := (*e.Store()).ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("claim event: %v", err)
	}
	if err := (*e.Store()).RecordPublishedEventRecipientProgress(context.Background(), work.ID, work.ClaimToken, subscription.ID,
		state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFailed, Attempts: 2,
			FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true,
			LastError: "temporary queue outage", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if err := (*e.Store()).FinishPublishedEvent(context.Background(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish failed event: %v", err)
	}
	if err := (*e.Store()).ReplayFailedPublishedEventRecipientForApp(context.Background(), (*e.Acct()).ID, appID,
		"orders", eventID, subscription.ID); err != nil {
		t.Fatalf("replay recipient: %v", err)
	}
	work, err = (*e.Store()).ClaimDuePublishedEvent(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("claim replay: %v", err)
	}
	if err := (*e.Store()).RecordPublishedEventRecipientProgress(context.Background(), work.ID, work.ClaimToken, subscription.ID,
		state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientEnqueued, Attempts: 3, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("record replay success: %v", err)
	}
	if err := (*e.Store()).FinishPublishedEvent(context.Background(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatalf("finish replay: %v", err)
	}

	missingIdentity := e.Do(t, http.MethodGet, "/v1/apps/fanout-history-api/event-deliveries/attempts?event_id="+eventID, nil, nil)
	if missingIdentity.Code != http.StatusBadRequest {
		t.Fatalf("missing event source status = %d, want 400; body=%s", missingIdentity.Code, missingIdentity.Body.String())
	}
	firstURL := url.Values{"event_source": {"orders"}, "event_id": {eventID}, "subscription_id": {subscription.ID}, "limit": {"2"}}
	first := e.Do(t, http.MethodGet, "/v1/apps/fanout-history-api/event-deliveries/attempts?"+firstURL.Encode(), nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first history page status = %d; body=%s", first.Code, first.Body.String())
	}
	var firstPage api.EventFanoutAttemptHistoryResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatalf("decode first history page: %v", err)
	}
	if len(firstPage.History) != 2 || firstPage.History[0].State != state.PublishedEventRecipientEnqueued ||
		firstPage.History[1].Action != state.EventFanoutAttemptActionReplay || firstPage.NextBefore == "" {
		t.Fatalf("first page = %+v, want successful outcome, replay request, and continuation cursor", firstPage)
	}
	secondURL := url.Values{
		"event_source": {"orders"}, "event_id": {eventID}, "subscription_id": {subscription.ID},
		"before": {firstPage.NextBefore},
	}
	second := e.Do(t, http.MethodGet, "/v1/apps/fanout-history-api/event-deliveries/attempts?"+secondURL.Encode(), nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second history page status = %d; body=%s", second.Code, second.Body.String())
	}
	var secondPage api.EventFanoutAttemptHistoryResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondPage); err != nil {
		t.Fatalf("decode second history page: %v", err)
	}
	if len(secondPage.History) != 1 || secondPage.History[0].State != state.PublishedEventRecipientFailed ||
		secondPage.History[0].LastError != "temporary queue outage" {
		t.Fatalf("second page = %+v, want original failed outcome", secondPage)
	}
	secondURL.Set("subscription_id", "different-subscription")
	mismatchedCursor := e.Do(t, http.MethodGet, "/v1/apps/fanout-history-api/event-deliveries/attempts?"+secondURL.Encode(), nil, nil)
	if mismatchedCursor.Code != http.StatusBadRequest {
		t.Fatalf("cursor with mismatched recipient filter status = %d, want 400; body=%s", mismatchedCursor.Code, mismatchedCursor.Body.String())
	}
}

func TestListEventFanoutHistoryCoverageAndCursorAfterCompaction(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.MustSeedApp(t, e, "history-compaction")
	ctx := context.Background()
	sub, _, err := (*e.Store()).UpsertEventSubscription(ctx, (*e.Acct()).ID, app, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).AppendEvent(ctx, "apid", "event.published", &(*e.Acct()).ID, json.RawMessage(`{"id":"history-compaction","source":"orders","type":"order.created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := (*e.Store()).ClaimDuePublishedEvent(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	const observations = 150
	for i := 1; i <= observations; i++ {
		if err := (*e.Store()).RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, sub.ID, state.PublishedEventRecipientProgress{State: "pending", Attempts: i, FailureCode: state.EventFanoutFailureCodeTargetLookupFailed, Retryable: true, LastError: "transient lookup", UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"event_source": {"orders"}, "event_id": {"history-compaction"}, "subscription_id": {sub.ID}, "limit": {"2"}}
	endpoint := "/v1/apps/history-compaction/event-deliveries/attempts?"
	first := e.Do(t, http.MethodGet, endpoint+query.Encode(), nil, nil)
	var page api.EventFanoutAttemptHistoryResponse
	if first.Code != http.StatusOK {
		t.Fatalf("status=%d %s", first.Code, first.Body.String())
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if first.Header().Get("Cache-Control") != "no-store" || page.Coverage != state.EventRoutingHistoryCoverage || len(page.Summaries) != 1 || page.Summaries[0].CompactedOutcomes == 0 || page.NextBefore == "" {
		t.Fatalf("coverage=%+v", page)
	}
	cursor := page.NextBefore
	if _, err := (*e.Store()).PruneEventFanoutHistory(ctx, time.Now().Add(31*24*time.Hour), 50); err != nil {
		t.Fatal(err)
	}
	query.Set("before", cursor)
	next := e.Do(t, http.MethodGet, endpoint+query.Encode(), nil, nil)
	if next.Code != http.StatusOK {
		t.Fatalf("cursor expired=%d %s", next.Code, next.Body.String())
	}
	if err := json.Unmarshal(next.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.History) != 0 || len(page.Summaries) != 1 || page.Summaries[0].CompactedOutcomes != observations-1 || page.Summaries[0].RetainedRecords != 1 || page.Summaries[0].ObservedOutcomes != observations {
		t.Fatalf("compacted page=%+v", page)
	}
	query.Set("subscription_id", "11111111-1111-1111-1111-111111111111")
	query.Del("before")
	isolated := e.Do(t, http.MethodGet, endpoint+query.Encode(), nil, nil)
	if err := json.Unmarshal(isolated.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if isolated.Code != http.StatusOK || len(page.History) != 0 || len(page.Summaries) != 0 {
		t.Fatalf("recipient metadata leaked=%+v", page)
	}
}

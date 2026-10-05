package main

// adr: 592

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventBacklogAPIRecoveryCountsAndCursors(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedAPIReceipt(t, e)
	// Same consumers, a second receipt: counts must exceed the recipient page.
	published := e.do(t, "POST", "/v1/events:publish", api.PublishEventRequest{ID: "receipt-2", Source: "orders", Type: "created", Data: json.RawMessage(`{}`)}, nil)
	if published.Code != 202 {
		t.Fatalf("publish=%d %s", published.Code, published.Body)
	}
	read := func(path string) api.EventBacklogResponse {
		t.Helper()
		rec := e.do(t, "GET", path, nil, nil)
		var result api.EventBacklogResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("backlog=%d %s", rec.Code, rec.Body)
		}
		return result
	}
	first := read("/v1/events/backlog?limit=1&consumer_limit=1")
	if len(first.Recipients) != 1 || len(first.Consumers) != 1 || first.Consumers[0].WaitingRecipients != 2 || first.NextAfter == "" || first.NextConsumersAfter == "" || first.Coverage != api.EventBacklogCoverage {
		t.Fatalf("first=%+v", first)
	}
	entry := first.Recipients[0]
	link, err := url.Parse(entry.ReceiptURL)
	if err != nil || link.Query().Get("source") != "orders" || link.Query().Get("id") != "receipt-1" || entry.FanoutHistoryURL == "" {
		t.Fatalf("links=%+v %v", entry, err)
	}
	ctx := context.Background()
	for range 3 {
		claim, err := e.store.ClaimDuePublishedEventRecipient(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if claim.Recipient.ID == entry.SubscriptionID {
			if err := e.store.FinishPublishedEventRecipient(ctx, claim, state.PublishedEventRecipientProgress{State: "enqueued", Attempts: claim.TotalAttempts, UpdatedAt: time.Now()}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	second := read("/v1/events/backlog?after=" + url.QueryEscape(first.NextAfter) + "&consumers_after=" + url.QueryEscape(first.NextConsumersAfter))
	if len(second.Recipients) != 5 || len(second.Consumers) != 2 || !second.WindowAt.Equal(first.WindowAt) || second.NextAfter != "" {
		t.Fatalf("second=%+v", second)
	}
	for _, r := range second.Recipients {
		if r.EventID == entry.EventID && r.SubscriptionID == entry.SubscriptionID {
			t.Fatal("recovered recipient returned")
		}
	}
	for _, query := range []string{"state=failed", "capacity_scope=unknown", "min_age_seconds=-1", "min_age_seconds=31536001", "limit=201", "consumer_limit=0", "after=bad", "after=" + url.QueryEscape(first.NextConsumersAfter), "state=pending&after=" + url.QueryEscape(first.NextAfter)} {
		rec := e.do(t, "GET", "/v1/events/backlog?"+query, nil, nil)
		if rec.Code != 400 {
			t.Fatalf("query=%s status=%d %s", query, rec.Code, rec.Body)
		}
	}
	other, err := e.store.CreateAccount(ctx, "backlog-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: other.ID, Slug: "foreign-backlog", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.store.UpsertEventSubscription(ctx, other.ID, app.ID, "secret", "created", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(ctx, "apid", "event.published", &other.ID, []byte(`{"id":"secret","source":"secret","type":"created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	for _, r := range read("/v1/events/backlog?account_id=" + other.ID).Recipients {
		if r.EventSource == "secret" {
			t.Fatal("account query escaped authentication")
		}
	}
	rec := e.do(t, "GET", "/v1/events/backlog?app=foreign-backlog", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("foreign app=%d %s", rec.Code, rec.Body)
	}
}

func TestEventBacklogScopes(t *testing.T) {
	for _, scope := range []string{api.ScopeAppsRead, api.ScopeEventsPublish} {
		t.Run(scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{scope})
			rec := e.do(t, "GET", "/v1/events/backlog", nil, nil)
			want := 200
			if scope == api.ScopeEventsPublish {
				want = 403
			}
			if rec.Code != want {
				t.Fatalf("status=%d %s", rec.Code, rec.Body)
			}
		})
	}
}

type deadlineBacklogStore struct{ state.Store }

func (s deadlineBacklogStore) EventBacklog(context.Context, string, state.EventBacklogQuery) (state.EventBacklog, error) {
	return state.EventBacklog{}, context.DeadlineExceeded
}
func TestEventBacklogTimeoutDoesNotReturnEmptySuccess(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.store = deadlineBacklogStore{Store: e.store}
	rec := e.do(t, "GET", "/v1/events/backlog", nil, nil)
	var p api.Problem
	if rec.Code != 503 || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Code != "event_backlog_read_timeout" {
		t.Fatalf("timeout=%d %s", rec.Code, rec.Body)
	}
}

func TestEventBacklogCursorAccountFilterAndWindowBinding(t *testing.T) {
	q := state.EventBacklogQuery{WindowAt: time.Now().UTC().Add(-time.Minute), AppID: uuid.NewString(), Filters: api.EventBacklogFilters{App: "orders"}}
	position := state.EventBacklogPosition{AcceptedAt: q.WindowAt.Add(-time.Minute), OutboxID: 12, SubscriptionID: "sub"}
	consumer := state.EventBacklogConsumerPosition{AppID: q.AppID, SubscriptionID: "sub"}
	after := encodeEventBacklogCursor("recipient", "owner", q, position, state.EventBacklogConsumerPosition{})
	consumersAfter := encodeEventBacklogCursor("consumer", "owner", q, state.EventBacklogPosition{}, consumer)
	values := url.Values{"after": {after}, "consumers_after": {consumersAfter}}
	parsed := state.EventBacklogQuery{AppID: q.AppID, Filters: q.Filters}
	if err := applyEventBacklogCursors(&parsed, values, "owner"); err != nil || parsed.After != position || parsed.ConsumersAfter != consumer || !parsed.WindowAt.Equal(q.WindowAt) {
		t.Fatalf("bound cursors=%+v %v", parsed, err)
	}
	if _, err := decodeEventBacklogCursor(after, "recipient", "foreign", q); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	changed := q
	changed.Filters.CapacityScope = "consumer"
	if _, err := decodeEventBacklogCursor(after, "recipient", "owner", changed); err == nil {
		t.Fatal("changed filters accepted")
	}
	changed = q
	changed.WindowAt = q.WindowAt.Add(time.Second)
	values.Set("consumers_after", encodeEventBacklogCursor("consumer", "owner", changed, state.EventBacklogPosition{}, consumer))
	parsed = state.EventBacklogQuery{AppID: q.AppID, Filters: q.Filters}
	if err := applyEventBacklogCursors(&parsed, values, "owner"); err == nil {
		t.Fatal("different windows accepted")
	}
}

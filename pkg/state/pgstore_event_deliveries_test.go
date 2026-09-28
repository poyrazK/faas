package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreListEventDeliveriesFiltersSourceAndEventID(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "event-deliveries", "event-deliveries")
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, event := range []struct {
		id               string
		source           string
		invocationSource state.InvocationSource
		headers          json.RawMessage
	}{
		{id: "evt-duplicate", source: "orders.us", invocationSource: state.InvocationAsyncInvoke,
			headers: json.RawMessage(`{"x-gregale-event-id":"evt-duplicate","x-gregale-event-source":"orders.us"}`)},
		{id: "evt-duplicate", source: "orders.eu", invocationSource: state.InvocationAsyncInvoke,
			headers: json.RawMessage(`{"x-gregale-event-id":"evt-duplicate","x-gregale-event-source":"orders.eu"}`)},
		{id: "evt-duplicate", source: "orders.eu", invocationSource: state.InvocationReplay,
			headers: json.RawMessage(`{"x-gregale-event-id":"evt-duplicate","x-gregale-event-source":"orders.eu"}`)},
		{id: "evt-other", source: "orders.us", invocationSource: state.InvocationAsyncInvoke,
			headers: json.RawMessage(`{"x-gregale-event-id":"evt-other","x-gregale-event-source":"orders.us"}`)},
		{invocationSource: state.InvocationReplay, headers: json.RawMessage(`{"x-user":"ordinary"}`)},
	} {
		_, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: accountID, Source: event.invocationSource,
			Headers: event.headers,
			DueAt:   now, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("enqueue %s/%s event delivery: %v", event.source, event.id, err)
		}
	}

	allSources, err := s.ListEventDeliveriesForApp(ctx, appID, 10, "", "", "evt-duplicate", "")
	if err != nil {
		t.Fatalf("list ID-only event deliveries: %v", err)
	}
	if len(allSources) != 3 {
		t.Fatalf("ID-only event deliveries = %d, want source collisions and event replay", len(allSources))
	}

	qualified, err := s.ListEventDeliveriesForApp(ctx, appID, 10, "", "orders.eu", "evt-duplicate", "")
	if err != nil {
		t.Fatalf("list source-qualified event deliveries: %v", err)
	}
	if len(qualified) != 2 {
		t.Fatalf("source-qualified event deliveries = %+v, want original and replay for orders.eu", qualified)
	}
	sources := map[state.InvocationSource]bool{}
	for _, delivery := range qualified {
		var headers map[string]string
		if err := json.Unmarshal(delivery.Headers, &headers); err != nil {
			t.Fatalf("decode qualified headers: %v", err)
		}
		if headers["x-gregale-event-source"] != "orders.eu" || headers["x-gregale-event-id"] != "evt-duplicate" {
			t.Fatalf("qualified delivery headers = %v", headers)
		}
		sources[delivery.Source] = true
	}
	if !sources[state.InvocationAsyncInvoke] || !sources[state.InvocationReplay] {
		t.Fatalf("qualified delivery sources = %v, want async_invoke and replay", sources)
	}

	page, err := s.ListEventDeliveriesForApp(ctx, appID, 1, "", "orders.eu", "evt-duplicate", "")
	if err != nil || len(page) != 1 {
		t.Fatalf("source-qualified cursor seed = %d rows, %v; want one row", len(page), err)
	}
	continued, err := s.ListEventDeliveriesForApp(ctx, appID, 1, page[0].ID, "orders.eu", "evt-duplicate", "")
	if err != nil || len(continued) != 1 {
		t.Fatalf("source-qualified cursor continuation = %d rows, %v; want the second source-qualified row", len(continued), err)
	}
	terminal, err := s.ListEventDeliveriesForApp(ctx, appID, 1, continued[0].ID, "orders.eu", "evt-duplicate", "")
	if err != nil || len(terminal) != 0 {
		t.Fatalf("source-qualified terminal cursor page = %d rows, %v; want empty page", len(terminal), err)
	}
}

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
		id     string
		source string
	}{
		{id: "evt-duplicate", source: "orders.us"},
		{id: "evt-duplicate", source: "orders.eu"},
		{id: "evt-other", source: "orders.us"},
	} {
		_, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: accountID, Source: state.InvocationAsyncInvoke,
			Headers: json.RawMessage(`{"x-gregale-event-id":"` + event.id + `","x-gregale-event-source":"` + event.source + `"}`),
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
	if len(allSources) != 2 {
		t.Fatalf("ID-only event deliveries = %d, want both source collisions", len(allSources))
	}

	qualified, err := s.ListEventDeliveriesForApp(ctx, appID, 10, "", "orders.eu", "evt-duplicate", "")
	if err != nil {
		t.Fatalf("list source-qualified event deliveries: %v", err)
	}
	if len(qualified) != 1 {
		t.Fatalf("source-qualified event deliveries = %+v, want only orders.eu", qualified)
	}
	var headers map[string]string
	if err := json.Unmarshal(qualified[0].Headers, &headers); err != nil {
		t.Fatalf("decode qualified headers: %v", err)
	}
	if headers["x-gregale-event-source"] != "orders.eu" || headers["x-gregale-event-id"] != "evt-duplicate" {
		t.Fatalf("qualified delivery headers = %v", headers)
	}

	page, err := s.ListEventDeliveriesForApp(ctx, appID, 1, "", "orders.eu", "evt-duplicate", "")
	if err != nil || len(page) != 1 {
		t.Fatalf("source-qualified cursor seed = %d rows, %v; want one row", len(page), err)
	}
	continued, err := s.ListEventDeliveriesForApp(ctx, appID, 1, page[0].ID, "orders.eu", "evt-duplicate", "")
	if err != nil || len(continued) != 0 {
		t.Fatalf("source-qualified cursor continuation = %d rows, %v; want empty page", len(continued), err)
	}
}

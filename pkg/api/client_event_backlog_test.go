package api

// adr: 617

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetEventBacklogFiltersAndMetadata(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/events/backlog" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		for k, v := range map[string]string{"app": "orders/?+", "subscription_id": "a&b", "state": "pending", "capacity_scope": "consumer", "min_age_seconds": "600", "after": "ebc1.a+b", "consumers_after": "ebc1.c/d", "limit": "17", "consumer_limit": "3"} {
			if got := r.URL.Query().Get(k); got != v {
				t.Errorf("%s=%q want %q", k, got, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"coverage":"captured_application_recipients","recipients":[{"event_id":"evt","waiting_reason":"capacity_consumer","capacity_deferrals":20}],"consumers":[{"waiting_recipients":9}],"next_after":"next","next_consumers_after":"consumers"}`))
	}))
	defer s.Close()
	r, err := NewClient(s.URL, "").GetEventBacklog(context.Background(), EventBacklogOptions{EventBacklogFilters: EventBacklogFilters{App: "orders/?+", SubscriptionID: "a&b", State: "pending", CapacityScope: "consumer", MinAgeSeconds: 600}, After: "ebc1.a+b", ConsumersAfter: "ebc1.c/d", Limit: 17, ConsumerLimit: 3})
	if err != nil || len(r.Recipients) != 1 || r.Recipients[0].CapacityDeferrals != 20 || len(r.Consumers) != 1 || r.Consumers[0].WaitingRecipients != 9 || r.NextAfter != "next" || r.NextConsumersAfter != "consumers" {
		t.Fatalf("result=%+v %v", r, err)
	}
}

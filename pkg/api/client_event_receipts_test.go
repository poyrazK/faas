package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

// ADR-619: identity and page cursors survive URL encoding and response decoding.
func TestGetEventReceiptSerializesIdentityAndPage(t *testing.T) {
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/events/receipt" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event_id":"evt/?+","event_source":"https://orders.example/a?b=1&c=2","recipient_count":1,"recipients":[{"subscription_id":"sub-1","routing":{"state":"enqueued","attempts":1},"execution":{"invocation_id":"inv-1","state":"completed","attempts":2},"recovery_actions":[]}],"next_after":"next"}`))
	}))
	defer server.Close()
	receipt, err := NewClient(server.URL, "").GetEventReceipt(context.Background(), "https://orders.example/a?b=1&c=2", "evt/?+", "erc1.a+/?", 17)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"source": {"https://orders.example/a?b=1&c=2"}, "id": {"evt/?+"}, "after": {"erc1.a+/?"}, "limit": {"17"}}
	if !reflect.DeepEqual(query, want) || receipt.EventID != "evt/?+" || receipt.Recipients[0].Execution.State != "completed" || receipt.NextAfter != "next" {
		t.Fatalf("query=%v receipt=%+v", query, receipt)
	}
}

func TestGetEventReceiptReplaysSerializesIdentityAndPage(t *testing.T) {
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/events/receipt/replays" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"original_invocation_id":"original","subscription_id":"sub/?+","replays":[{"invocation_id":"child","replayed_from_invocation_id":"parent","state":"completed"}],"next_after":"next"}`))
	}))
	defer server.Close()
	history, err := NewClient(server.URL, "").GetEventReceiptReplays(context.Background(), "https://orders.example/a?b=1&c=2", "evt/?+", "sub/?+", "err1.a+/?", 17)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"source": {"https://orders.example/a?b=1&c=2"}, "id": {"evt/?+"}, "subscription_id": {"sub/?+"}, "after": {"err1.a+/?"}, "limit": {"17"}}
	if !reflect.DeepEqual(query, want) || len(history.Replays) != 1 || history.Replays[0].ReplayedFromInvocationID != "parent" || history.NextAfter != "next" {
		t.Fatalf("query=%v history=%+v", query, history)
	}
}

func TestGetEventReceiptAttemptsSerializesIdentityAndPage(t *testing.T) {
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/events/receipt/attempts" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"coverage":"recorded_attempts_only","attempts":[{"id":12,"invocation_id":"original","replay_generation":2,"attempt":1,"outcome":"unknown"}],"next_after":"next"}`))
	}))
	defer server.Close()
	history, err := NewClient(server.URL, "").GetEventReceiptAttempts(context.Background(), "https://orders.example/a?b=1&c=2", "evt/?+", "sub/?+", "era1.a+/?", 17)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"source": {"https://orders.example/a?b=1&c=2"}, "id": {"evt/?+"}, "subscription_id": {"sub/?+"}, "after": {"era1.a+/?"}, "limit": {"17"}}
	if !reflect.DeepEqual(query, want) || len(history.Attempts) != 1 || history.Attempts[0].ReplayGeneration != 2 || history.Attempts[0].Outcome != "unknown" || history.NextAfter != "next" {
		t.Fatalf("query=%v history=%+v", query, history)
	}
}

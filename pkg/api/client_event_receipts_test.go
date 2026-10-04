package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

// ADR-582: identity and page cursors survive URL encoding and response decoding.
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

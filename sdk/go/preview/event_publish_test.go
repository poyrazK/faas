package preview_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
	"github.com/poyrazK/faas/sdk/go/preview"
)

// Account-level event publication is a preview operation: it lives on
// preview.Client and posts the envelope to the account routes.
func TestPreviewClientPublishesAccountEvents(t *testing.T) {
	public := reflect.TypeOf(&faas.Client{})
	for _, name := range []string{"PublishEvent", "PublishEventBatch"} {
		if _, ok := public.MethodByName(name); ok {
			t.Errorf("faas.Client exposes the preview method %s", name)
		}
	}
	var bodies = map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies[r.Method+" "+r.URL.Path] = body
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/events:publish-batch" {
			_, _ = w.Write([]byte(`{"results":[{"index":0,"status":"accepted","retryable":false,"receipt":{"receipt_url":"/v1/events/receipt?id=evt-2&source=orders","id":"evt-2","accepted_at":"2026-10-26T00:00:00Z"}}]}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"receipt_url":"/v1/events/receipt?id=evt-1&source=orders","id":"evt-1","accepted_at":"2026-10-26T00:00:00Z","account_id":"acct"}`))
	}))
	defer server.Close()
	c, err := preview.NewClient(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	event := preview.PublishEventRequest{ID: "evt-1", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order_id":"o-1"}`)}
	receipt, err := c.PublishEvent(context.Background(), event)
	if err != nil || receipt.ID != "evt-1" || receipt.ReceiptURL == "" {
		t.Fatalf("PublishEvent = %+v, %v", receipt, err)
	}
	if got := bodies["POST /v1/events:publish"]; got["source"] != "orders" || got["type"] != "order.created" {
		t.Fatalf("publish body = %v", got)
	}
	event.ID = "evt-2"
	batch, err := c.PublishEventBatch(context.Background(), preview.PublishEventBatchRequest{Events: []preview.PublishEventRequest{event}})
	if err != nil || len(batch.Results) != 1 || batch.Results[0].Status != "accepted" || batch.Results[0].Receipt.ID != "evt-2" {
		t.Fatalf("PublishEventBatch = %+v, %v", batch, err)
	}
	if got := bodies["POST /v1/events:publish-batch"]; len(got["events"].([]any)) != 1 {
		t.Fatalf("batch body = %v", got)
	}
}

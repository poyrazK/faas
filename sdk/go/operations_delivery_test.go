// adr: 521
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestOperationDeliveryReceiptWireContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/exports/operations/operation/delivery":
			writeOperationJSON(w, 200, `{"operation_id":"operation","business_state":"succeeded","state":"dead","replay_generation":0,"last_response_code":422,"error_code":"receiver_http_error"}`)
		case "GET /v1/apps/exports/operations/operation/delivery-attempts":
			if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("cursor") != "opaque+/=" {
				t.Error("attempt selectors lost")
			}
			writeOperationJSON(w, 200, `{"operation_id":"operation","delivery_id":"delivery","attempts":[],"next_cursor":"next"}`)
		case "POST /v1/apps/exports/operations/operation/delivery-retries":
			var req faas.OperationDeliveryRetryRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedReplayGeneration == nil || *req.ExpectedReplayGeneration != 0 || req.RetryID != "stable" || req.DeliveryID != "delivery" {
				t.Error("explicit zero or retry identity lost")
			}
			writeOperationJSON(w, 200, `{"operation_id":"operation","retry_id":"stable","delivery_id":"delivery","expected_replay_generation":0,"replay_generation":1,"state":"queued"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := operationClient(t, server)
	report, err := client.GetOperationDelivery(context.Background(), "exports", "operation")
	if err != nil || report.BusinessState != faas.OperationSucceeded || report.State != "dead" || report.ReplayGeneration == nil || *report.ReplayGeneration != 0 {
		t.Fatalf("report %+v %v", report, err)
	}
	page, err := client.GetOperationDeliveryAttempts(context.Background(), "exports", "operation", 1, "opaque+/=")
	if err != nil || page.NextCursor != "next" {
		t.Fatalf("page %+v %v", page, err)
	}
	generation := 0
	r, err := client.RetryOperationDeliveryWithReceipt(context.Background(), "exports", "operation", faas.OperationDeliveryRetryRequest{RetryID: "stable", DeliveryID: "delivery", ExpectedReplayGeneration: &generation})
	if err != nil || r.State != "queued" || r.ReplayGeneration != 1 {
		t.Fatalf("receipt %+v %v", r, err)
	}
}

// adr: 20261025090000
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestDeploymentPromotionReceiptRead(t *testing.T) {
	id := strings.Repeat("a", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/alpha/deployment-promotions/"+id || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Deployment-Promotion-Attempt-ID") != "" {
			t.Error("receipt read carried a write or wrong selector")
		}
		_ = json.NewEncoder(w).Encode(faas.DeploymentPromotionReceipt{AttemptID: id, DeploymentID: "candidate", Scope: "default"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "read-token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.GetDeploymentPromotionReceipt(faas.WithIdempotencyKey(context.Background(), "write-key"), "alpha", id)
	if err != nil || got.AttemptID != id || got.DeploymentID != "candidate" {
		t.Fatalf("receipt=%+v %v", got, err)
	}
}

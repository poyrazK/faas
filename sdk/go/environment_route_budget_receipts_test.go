// adr: 20261024210000
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEnvironmentRouteBudgetReceiptRead(t *testing.T) {
	for _, environment := range []string{"", "staging"} {
		t.Run(environment, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/alpha/route-budgets/receipts/request-id" || r.Header.Get("Idempotency-Key") != "" {
					t.Error("receipt lookup carried a write or wrong target")
				}
				if environment == "" && r.URL.Query().Get("scope") != "default" || environment != "" && r.URL.Query().Get("environment") != environment {
					t.Error("receipt selector lost")
				}
				_ = json.NewEncoder(w).Encode(faas.EnvironmentRouteBudgetReceipt{RequestID: "request-id", Revision: 1, AppRevision: 0, BudgetSource: "environment_policy"})
			}))
			defer server.Close()
			client, err := faas.NewClient(server.URL, "read-token")
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.GetEnvironmentRouteBudgetReceipt(faas.WithIdempotencyKey(context.Background(), "write-context-must-not-propagate"), "alpha", environment, "request-id")
			if err != nil || out.Revision != 1 || out.AppRevision != 0 || out.BudgetSource != "environment_policy" {
				t.Fatalf("receipt=%+v err=%v", out, err)
			}
		})
	}
}

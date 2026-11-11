// adr: 20261024210000
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnvironmentBudgetReceiptHashCompatibility(t *testing.T) {
	rev := int64(0)
	req := SetEnvironmentRouteBudgetsRequest{AppID: "app", RequestID: "request", ExpectedRevision: &rev, ExpectedAppRevision: &rev, Routes: []RouteMonitorRoute{}}
	old, _ := json.Marshal(struct {
		Scope   string
		Request SetEnvironmentRouteBudgetsRequest
	}{"default", req})
	digest := sha256.Sum256(old)
	got, err := EnvironmentRouteBudgetRequestHash("default", req)
	if err != nil || got != hex.EncodeToString(digest[:]) {
		t.Fatal("existing durable fingerprint changed")
	}
}

func TestEnvironmentBudgetReceiptClientRead(t *testing.T) {
	for _, env := range []string{"", "staging"} {
		t.Run(env, func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/apps/alpha/route-budgets/receipts/receipt-id" || r.Header.Get("Idempotency-Key") != "" {
					t.Error("receipt lookup changed method/path or carried write authority")
				}
				if env == "" && r.URL.Query().Get("scope") != "default" || env != "" && r.URL.Query().Get("environment") != env {
					t.Error("scope selector lost")
				}
				_ = json.NewEncoder(w).Encode(EnvironmentRouteBudgetReceipt{RequestID: "receipt-id", Revision: 1})
			}))
			defer b.Close()
			got, err := NewClient(b.URL, "read-token").GetEnvironmentRouteBudgetReceipt(ContextWithIdempotencyKey(context.Background(), "must-not-write"), "alpha", env, "receipt-id")
			if err != nil || got.Revision != 1 {
				t.Fatal(err)
			}
		})
	}
}

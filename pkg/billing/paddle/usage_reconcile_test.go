// adr: 032
package paddle

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	paddlesdk "github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPaddleRecoveryAndReconciliationUseTransactionCustomData(t *testing.T) {
	ctx := context.Background()
	window := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	acct := state.Account{
		ID: "acct-reconcile", Email: "reconcile@example.com", Plan: api.PlanHobby,
		ProviderCustomerID: "ctm_reconcile", StripeSubscriptionItem: "sub_reconcile",
	}
	idem := fmt.Sprintf("faas-overage-%s-%s", acct.ID, window.Format(time.RFC3339))
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			http.Error(w, "recovery must not duplicate the transaction", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
  "data": [{
    "id": "txn_existing",
    "customer_id": %q,
    "custom_data": {
      "faas_account_id": %q,
      "faas_paddle_idem_key": %q,
      "window_start": %q,
      "mb_seconds": "123"
    }
  }],
  "meta": {"pagination": {"per_page": 30, "has_more": false, "estimated_total": 1}}
}`, acct.ProviderCustomerID, acct.ID, idem, window.Format(time.RFC3339))
	}))
	defer server.Close()

	sdk, err := paddlesdk.New("test-key", paddlesdk.WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	dedupe := state.NewMemStore()
	if claimed, err := dedupe.ClaimPaddleOverageWindow(ctx, acct.ID, window, "old-pod", time.Minute); err != nil || !claimed {
		t.Fatalf("seed claim = (%v, %v)", claimed, err)
	}
	if err := dedupe.CompletePaddleOverageWindow(ctx, acct.ID, window, 123); err != nil {
		t.Fatal(err)
	}
	p := &Provider{
		apiKey: "test-key", client: sdk, dedupe: dedupe, now: time.Now,
		catalog: &priceCatalog{planMonthly: map[api.Plan]string{}, planOverage: map[api.Plan]string{api.PlanHobby: "pri_overage"}, planCustomers: map[api.Plan]string{}},
	}
	if err := p.flushOverageLocked(ctx, acct, window, 123); err != nil {
		t.Fatalf("recover overage: %v", err)
	}
	if posts != 0 {
		t.Fatalf("Paddle POSTs = %d, want 0 after provider-side recovery", posts)
	}

	total, err := p.ReconcileUsage(ctx, acct, window, window.Add(time.Hour))
	if err != nil || total != 123 {
		t.Fatalf("ReconcileUsage = (%d, %v), want (123, nil)", total, err)
	}
}

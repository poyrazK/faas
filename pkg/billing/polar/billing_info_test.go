// adr: 20261031090658 — the business name reaches Polar invoices.
package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// PATCH /v1/account/billing reaches Polar as the customer's name, the name
// Polar prints on invoices. The free-form address and tax ID stay local:
// Polar needs a structured address with a country to accept either.
func TestSyncCustomerBillingInfoSendsTheBusinessName(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/customers/cust-1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"cust-1"}`)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	acct := state.Account{ID: "acct-1", ProviderCustomerID: "cust-1", BusinessName: " Acme GmbH ",
		BillingAddress: "Hauptstr. 1, 10115 Berlin", TaxID: "DE123456789"}
	if err := p.SyncCustomerBillingInfo(context.Background(), acct); err != nil {
		t.Fatal(err)
	}
	acct.BusinessName = ""
	if err := p.SyncCustomerBillingInfo(context.Background(), acct); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || len(bodies[0]) != 1 || bodies[0]["name"] != "Acme GmbH" {
		t.Fatalf("customer updates = %v, want one {name: Acme GmbH}", bodies)
	}
	if err := p.SyncCustomerBillingInfo(context.Background(), state.Account{ID: "acct-2", BusinessName: "Acme"}); err == nil {
		t.Fatal("synced an account without a Polar customer")
	}
}

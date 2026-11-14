// spec: §10 — customer billing identity is synchronized to the billing provider.
package stripe

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/client"
)

func TestStripeCustomerParamsIncludesBillingIdentity(t *testing.T) {
	params := stripeCustomerParams(state.Account{
		ID:             "acct-1",
		Email:          "owner@example.com",
		BusinessName:   "Acme GmbH",
		BillingAddress: "Hauptstrasse 1, Berlin",
		TaxID:          "DE123456789",
	})
	if params.Name == nil || *params.Name != "Acme GmbH" {
		t.Fatalf("customer name = %v", params.Name)
	}
	if params.Address == nil || params.Address.Line1 == nil || *params.Address.Line1 != "Hauptstrasse 1, Berlin" {
		t.Fatalf("customer address = %+v", params.Address)
	}
	if len(params.TaxIDData) != 1 || params.TaxIDData[0].Type == nil || *params.TaxIDData[0].Type != "eu_vat" || params.TaxIDData[0].Value == nil || *params.TaxIDData[0].Value != "DE123456789" {
		t.Fatalf("customer tax ids = %+v", params.TaxIDData)
	}
}

func TestSyncCustomerBillingInfoClearsProviderAddress(t *testing.T) {
	var mu sync.Mutex
	address := "Old street"
	cleared := false
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers/cus_test":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if values, present := r.Form["address[line1]"]; present {
				address = values[0]
			}
			if values, present := r.Form["address"]; present && values[0] == "" {
				address, cleared = "", true
			}
			_, _ = fmt.Fprintf(w, `{"id":"cus_test","object":"customer","address":{"line1":%q}}`, address)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/cus_test/tax_ids":
			_, _ = w.Write([]byte(`{"object":"list","data":[],"has_more":false}`))
		default:
			t.Errorf("unexpected Stripe request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer endpoint.Close()
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: endpoint.URL, HTTPClient: endpoint.Client()})
	c := &Client{api: client.New("offline-test-key", &stripe.Backends{API: backend})}
	account := state.Account{ID: "test-account", Email: "billing@example.com", ProviderCustomerID: "cus_test", BillingAddress: "New street"}
	if err := c.SyncCustomerBillingInfo(t.Context(), account); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if address != "New street" || cleared {
		t.Error("nonempty address was not sent as an update")
	}
	mu.Unlock()
	account.BillingAddress = ""
	// The clear is safe to repeat, including a retry after the provider applied
	// it but the response was lost.
	for range 2 {
		if err := c.SyncCustomerBillingInfo(t.Context(), account); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if address != "" || !cleared {
		t.Fatalf("explicit clear was omitted: address=%q clear=%v", address, cleared)
	}
}

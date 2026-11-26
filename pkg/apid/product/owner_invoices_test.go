package product

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/statementinvoices"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

type ownerInvoiceTestHost struct {
	statementinvoices.Host
	store   state.Store
	account string
}

func (h ownerInvoiceTestHost) Store() state.Store                                    { return h.store }
func (h ownerInvoiceTestHost) Audit(context.Context, string, string, map[string]any) {}
func (h ownerInvoiceTestHost) SendEmail(context.Context, string, string, string, string) error {
	return nil
}

func (h ownerInvoiceTestHost) OpenSecret(data []byte) (string, []byte, error) {
	return api.BillingProviderKeyNamespace(state.BillingProviderStripe, h.account), data, nil
}

func newOwnerInvoiceTestService(t *testing.T, store *state.MemStore, account string, provider http.RoundTripper) *statementinvoices.Service {
	t.Helper()
	setOwnerInvoiceTestConnection(t, store, account, state.CollectionNone)
	return &statementinvoices.Service{
		Host:   ownerInvoiceTestHost{store: store, account: account},
		Stripe: &stripeinvoice.Client{BaseURL: "https://provider.test", HTTP: &http.Client{Transport: provider}},
	}
}

func setOwnerInvoiceTestConnection(t *testing.T, store *state.MemStore, account, collection string) {
	t.Helper()
	if _, err := store.PutBillingConnection(t.Context(), state.BillingConnection{
		AccountID: account, Provider: state.BillingProviderStripe, SealedSecret: []byte("rk_test_invoice"), KeyLast4: "oice",
		Collection: collection,
	}); err != nil {
		t.Fatal(err)
	}
}

type ownerInvoiceFakeProvider struct {
	t        *testing.T
	key      string
	created  int
	requests int
	amount   string
	payment  string
}

func (p *ownerInvoiceFakeProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(body string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoices":
		p.requests++
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			p.t.Fatal("provider invoice request omitted the idempotency key")
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("customer") != "cus_feeonly" {
			p.t.Fatalf("provider request did not retain the reviewed customer: %v", err)
		}
		if p.key == "" {
			p.key = key
			p.created++
			// The provider recorded the invoice, but the caller lost the response.
			return nil, io.ErrUnexpectedEOF
		}
		if p.key != key {
			p.t.Fatal("resuming would create another invoice with a different key")
		}
		return reply(`{"id":"in_feeonly","livemode":false,"total":0}`)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices/in_feeonly":
		if p.payment == "" {
			return nil, io.ErrUnexpectedEOF
		}
		return reply(p.payment)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoiceitems":
		return reply(`{"data":[],"has_more":false}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoiceitems":
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		p.amount = r.Form.Get("amount")
		return reply(`{"id":"ii_feeonly"}`)
	default:
		p.t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		return nil, io.ErrUnexpectedEOF
	}
}

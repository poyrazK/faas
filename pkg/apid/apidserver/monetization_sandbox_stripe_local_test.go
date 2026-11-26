//go:build monetization_sandbox

package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMonetizationReviewedStripeBillingMonthsLocally(t *testing.T) {
	const key = "rk_test_reviewed_months_fixture"
	testMode := false
	customer := sandboxStripeCustomer{
		ID: "cus_ReviewedMonths", Email: "billing@example.test", Livemode: &testMode, PaymentMethodID: "pm_test_card",
	}
	provider := &reviewedStripeLocalProvider{customer: customer, invoices: make(map[string]*reviewedStripeLocalInvoice)}
	srv := httptest.NewServer(provider)
	t.Cleanup(srv.Close)
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	calls := &sandboxStripeIssueCalls{next: client.HTTP.Transport}
	client.HTTP.Transport = calls
	stripe := sandboxHTTP{t: t, base: srv.URL, form: true, header: http.Header{"Authorization": {"Bearer " + key}}}
	verifyReviewedStripeBillingMonths(t, client, calls, stripe, key, customer)
}

type reviewedStripeLocalProvider struct {
	mu       sync.Mutex
	customer sandboxStripeCustomer
	invoices map[string]*reviewedStripeLocalInvoice
	nextID   int
}

type reviewedStripeLocalInvoice struct {
	doc   map[string]any
	lines []map[string]any
	total int64
}

func (f *reviewedStripeLocalProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer rk_test_") || r.ParseForm() != nil {
		http.Error(w, "invalid Stripe test request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/account":
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "acct_ReviewedMonths"})
	case "GET /v1/customers/" + f.customer.ID:
		_ = json.NewEncoder(w).Encode(f.customer)
	case "GET /v1/invoices":
		rows := make([]map[string]any, 0, len(f.invoices))
		for _, invoice := range f.invoices {
			if customer := r.Form.Get("customer"); customer == "" || invoice.doc["customer"] == customer {
				rows = append(rows, invoice.doc)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "has_more": false})
	case "POST /v1/invoices":
		if r.Form.Get("customer") != f.customer.ID || r.Form.Get("auto_advance") != "false" ||
			r.Header.Get("Idempotency-Key") == "" {
			http.Error(w, "expected a draft for the test customer", http.StatusBadRequest)
			return
		}
		f.nextID++
		id := fmt.Sprintf("in_month%d", f.nextID)
		doc := map[string]any{
			"id": id, "status": state.InvoicePaymentDraft, "customer": f.customer.ID,
			"customer_email": f.customer.Email, "currency": r.Form.Get("currency"), "livemode": false,
			"auto_advance": false, "collection_method": "charge_automatically",
			"total": 0, "amount_due": 0, "amount_remaining": 0,
			"metadata": map[string]string{
				"gregale_statement_id": r.Form.Get("metadata[gregale_statement_id]"),
				"gregale_source":       r.Form.Get("metadata[gregale_source]"),
			},
			"status_transitions": map[string]int64{},
		}
		f.invoices[id] = &reviewedStripeLocalInvoice{doc: doc, lines: make([]map[string]any, 0)}
		_ = json.NewEncoder(w).Encode(doc)
	case "GET /v1/invoiceitems":
		invoice := f.invoices[r.Form.Get("invoice")]
		if invoice == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": invoice.lines, "has_more": false})
	case "POST /v1/invoiceitems":
		invoice := f.invoices[r.Form.Get("invoice")]
		amount, err := strconv.ParseInt(r.Form.Get("amount"), 10, 64)
		if invoice == nil || err != nil || invoice.doc["status"] != state.InvoicePaymentDraft {
			http.Error(w, "invalid Stripe invoice item", http.StatusBadRequest)
			return
		}
		line := map[string]any{
			"id":       fmt.Sprintf("ii_%d", len(invoice.lines)+1),
			"metadata": map[string]string{"gregale_line": r.Form.Get("metadata[gregale_line]")},
		}
		invoice.lines = append(invoice.lines, line)
		invoice.total += amount
		invoice.doc["total"], invoice.doc["amount_due"], invoice.doc["amount_remaining"] = invoice.total, invoice.total, invoice.total
		_ = json.NewEncoder(w).Encode(line)
	default:
		f.invoiceRequest(w, r)
	}
}

func (f *reviewedStripeLocalProvider) invoiceRequest(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, "/v1/invoices/")
	id, action, _ := strings.Cut(path, "/")
	invoice := f.invoices[id]
	if !ok || invoice == nil {
		http.NotFound(w, r)
		return
	}
	doc := invoice.doc
	balance := doc["status_transitions"].(map[string]int64)
	draft := doc["status"] == state.InvoicePaymentDraft
	open := doc["status"] == state.InvoicePaymentOpen
	key := r.Header.Get("Idempotency-Key")
	switch {
	case r.Method == http.MethodGet && action == "":
	case r.Method == http.MethodDelete && action == "" && draft:
		delete(f.invoices, id)
	case r.Method == http.MethodPost && action == "" && key != "" && draft &&
		r.Form.Get("collection_method") == "send_invoice" && r.Form.Get("auto_advance") == "false":
		dueAt, err := strconv.ParseInt(r.Form.Get("due_date"), 10, 64)
		if err != nil {
			http.Error(w, "invalid due date", http.StatusBadRequest)
			return
		}
		doc["due_date"], doc["collection_method"] = dueAt, "send_invoice"
	case r.Method == http.MethodPost && action == "finalize" && key != "" && draft &&
		doc["collection_method"] == "send_invoice" && r.Form.Get("auto_advance") == "false":
		doc["status"], doc["hosted_invoice_url"] = state.InvoicePaymentOpen, "https://invoice.stripe.example/i/"+id
		balance["finalized_at"] = time.Now().Unix()
	case r.Method == http.MethodPost && action == "send" && key != "" && open:
	case r.Method == http.MethodPost && action == "pay" && open && r.Form.Get("payment_method") == f.customer.PaymentMethodID:
		doc["status"], doc["amount_paid"], doc["amount_remaining"] = state.InvoicePaymentPaid, invoice.total, 0
		balance["paid_at"] = time.Now().Unix()
	case r.Method == http.MethodPost && action == "void" && open:
		doc["status"] = state.InvoicePaymentVoid
	default:
		http.Error(w, "unexpected Stripe invoice operation", http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(doc)
}

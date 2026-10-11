package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeStripe records invoices and items like Stripe would, and can fail
// the next item creation to exercise resumed exports.
type fakeStripe struct {
	mu          sync.Mutex
	invoices    int
	items       map[string][]string // invoice -> gregale_line values
	amounts     []string
	failNextAdd bool
}

func (f *fakeStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer rk_test_") {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":{"message":"Invalid API Key provided"}}`)
		return
	}
	_ = r.ParseForm()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		_, _ = fmt.Fprint(w, `{"data":[],"has_more":false}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoices":
		f.invoices++
		_, _ = fmt.Fprintf(w, `{"id":"in_%d","livemode":false,"total":0}`, f.invoices)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoiceitems":
		var data []string
		for i, line := range f.items[r.URL.Query().Get("invoice")] {
			data = append(data, fmt.Sprintf(`{"id":"ii_%d","metadata":{"gregale_line":%q}}`, i, line))
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false}`, strings.Join(data, ","))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoiceitems":
		if f.failNextAdd {
			f.failNextAdd = false
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error":{"message":"try again"}}`)
			return
		}
		if f.items == nil {
			f.items = map[string][]string{}
		}
		invoice := r.PostForm.Get("invoice")
		f.items[invoice] = append(f.items[invoice], r.PostForm.Get("metadata[gregale_line]"))
		f.amounts = append(f.amounts, r.PostForm.Get("amount")+":"+r.PostForm.Get("description"))
		_, _ = fmt.Fprint(w, `{"id":"ii"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func withBillingKeys(t *testing.T) {
	t.Helper()
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	prevRecipient, prevIdentities := mfaRecipient, mfaIdentities
	mfaRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	t.Cleanup(func() { mfaRecipient, mfaIdentities = prevRecipient, prevIdentities })
}

// adr: 976 — a finalized statement becomes one draft Stripe invoice; a
// failed export resumes the same invoice and never duplicates items.
func TestExportAPIConsumerUsageStatementToStripe(t *testing.T) {
	withBillingKeys(t)
	e := setup(t, api.PlanHobby)
	stripe := &fakeStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	e.s.stripeInvoices = &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}

	if res := e.do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutStripeConnectionRequest{SecretKey: "pk_test_nope"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publishable key: %d %s", res.Code, res.Body)
	}
	if res := e.do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutStripeConnectionRequest{SecretKey: "sk_test_rejected"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected key: %d %s", res.Code, res.Body)
	}
	res := e.do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutStripeConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil)
	var conn api.BillingConnectionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &conn); err != nil || res.Code != http.StatusOK || conn.KeyLast4 != "WXYZ" || conn.Livemode {
		t.Fatalf("connect: %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "rk_test_51abcd") {
		t.Fatal("connection response leaked the key")
	}

	mustSeedApp(t, e, "stripe-export")
	created := e.do(t, http.MethodPost, "/v1/apps/stripe-export/consumers", api.CreateAPIConsumerRequest{ExternalRef: "cus_ABC123", Name: "Paying Customer"}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	if rate := e.do(t, http.MethodPost, "/v1/apps/stripe-export/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &minute}, nil); rate.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", rate.Code, rate.Body)
	}
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: e.acct.ID,
		AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 3, BillableUnits: 3}); err != nil {
		t.Fatal(err)
	}
	statements := "/v1/apps/stripe-export/consumers/" + consumer.ID + "/usage-statements"
	end := minute.Add(time.Hour)
	res = e.do(t, http.MethodPost, statements, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}, nil)
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &statement); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("statement: %d %s", res.Code, res.Body)
	}
	exportPath := statements + "/" + statement.ID + "/stripe-invoice"
	if res := e.do(t, http.MethodPost, exportPath, struct{}{}, nil); res.Code != http.StatusConflict {
		t.Fatalf("draft export: %d %s, want 409", res.Code, res.Body)
	}
	if res := e.do(t, http.MethodPost, statements+"/"+statement.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", res.Code, res.Body)
	}

	stripe.failNextAdd = true
	if res := e.do(t, http.MethodPost, exportPath, struct{}{}, nil); res.Code != http.StatusBadGateway {
		t.Fatalf("export with a Stripe failure: %d %s, want 502", res.Code, res.Body)
	}
	res = e.do(t, http.MethodPost, exportPath, struct{}{}, nil)
	var export api.StatementInvoiceExportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &export); err != nil || res.Code != http.StatusOK {
		t.Fatalf("resumed export: %d %s", res.Code, res.Body)
	}
	if export.Status != "completed" || export.InvoiceID != "in_1" || export.Customer != "cus_ABC123" || export.AmountMinor != 4 ||
		!strings.Contains(export.DashboardURL, "/test/invoices/in_1") {
		t.Fatalf("export = %+v", export)
	}
	// 3 units at 0.0125 EUR = 3.75 cents: one 4-cent item, no rounding item.
	if stripe.invoices != 1 || len(stripe.items["in_1"]) != 1 || stripe.amounts[0] != "4:3 usage units at 0.0125 EUR each" {
		t.Fatalf("stripe saw invoices=%d items=%v amounts=%v", stripe.invoices, stripe.items, stripe.amounts)
	}
	if res := e.do(t, http.MethodPost, exportPath, struct{}{}, nil); res.Code != http.StatusOK || stripe.invoices != 1 || len(stripe.items["in_1"]) != 1 {
		t.Fatalf("replay: %d, invoices=%d items=%v", res.Code, stripe.invoices, stripe.items)
	}
	handoff := e.do(t, http.MethodGet, statements+"/"+statement.ID+"/handoff", nil, nil)
	if handoff.Code != http.StatusOK || !strings.Contains(handoff.Body.String(), `"in_1"`) {
		t.Fatalf("handoff: %d %s", handoff.Code, handoff.Body)
	}
	if res := e.do(t, http.MethodGet, exportPath, nil, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"completed"`) {
		t.Fatalf("get export: %d %s", res.Code, res.Body)
	}
	if res := e.do(t, http.MethodDelete, "/v1/account/billing-connections/stripe", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", res.Code, res.Body)
	}
}

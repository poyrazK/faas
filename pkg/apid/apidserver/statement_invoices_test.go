package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
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
	// payments maps an invoice ID to the invoice JSON GET /v1/invoices/{id}
	// returns (ADR-1504); unknown invoices are 404.
	payments map[string]string
	// trackDrafts reports created invoices as drafts until finalized
	// (ADR-1311); collection and finalized record what Stripe was asked.
	trackDrafts      bool
	failNextFinalize bool
	collection       map[string]string
	finalized        map[string]string
}

// setPayment sets what Stripe reports for an invoice's payment.
func (f *fakeStripe) setPayment(invoiceID, invoiceJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.payments == nil {
		f.payments = map[string]string{}
	}
	f.payments[invoiceID] = invoiceJSON
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
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/invoices/"):
		invoice, ok := f.payments[strings.TrimPrefix(r.URL.Path, "/v1/invoices/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"code":"resource_missing","message":"No such invoice"}}`)
			return
		}
		_, _ = fmt.Fprint(w, invoice)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		_, _ = fmt.Fprint(w, `{"data":[],"has_more":false}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/finalize"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/invoices/"), "/finalize")
		if f.failNextFinalize {
			f.failNextFinalize = false
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error":{"message":"try again"}}`)
			return
		}
		if f.finalized == nil {
			f.finalized = map[string]string{}
		}
		f.finalized[id] = r.PostForm.Get("auto_advance")
		f.payments[id] = fmt.Sprintf(`{"id":%q,"status":"open","amount_paid":0,"amount_remaining":4,"status_transitions":{"paid_at":null}}`, id)
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"open"}`, id)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoices":
		f.invoices++
		id := fmt.Sprintf("in_%d", f.invoices)
		if f.trackDrafts {
			if f.payments == nil {
				f.payments = map[string]string{}
			}
			if f.collection == nil {
				f.collection = map[string]string{}
			}
			f.payments[id] = fmt.Sprintf(`{"id":%q,"status":"draft","amount_paid":0,"amount_remaining":4,"status_transitions":{"paid_at":null}}`, id)
			f.collection[id] = r.PostForm.Get("collection_method") + "/" + r.PostForm.Get("days_until_due")
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"livemode":false,"total":0}`, id)
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
	prevRecipient, prevIdentities := *apidserver.MfaRecipient, *apidserver.MfaIdentities
	*apidserver.MfaRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	t.Cleanup(func() { *apidserver.MfaRecipient, *apidserver.MfaIdentities = prevRecipient, prevIdentities })
}

// adr: 1502 — a finalized statement becomes one draft Stripe invoice; a
// failed export resumes the same invoice and never duplicates items.
func TestExportAPIConsumerUsageStatementToStripe(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &fakeStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	(*e.S()).StatementInvoices().Stripe = &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}

	if res := e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "pk_test_nope"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publishable key: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "sk_test_rejected"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected key: %d %s", res.Code, res.Body)
	}
	res := e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil)
	var conn api.BillingConnectionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &conn); err != nil || res.Code != http.StatusOK || conn.KeyLast4 != "WXYZ" || conn.Livemode {
		t.Fatalf("connect: %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "rk_test_51abcd") {
		t.Fatal("connection response leaked the key")
	}

	apidserver.MustSeedApp(t, e, "stripe-export")
	created := e.Do(t, http.MethodPost, "/v1/apps/stripe-export/consumers", api.CreateAPIConsumerRequest{ExternalRef: "cus_ABC123", Name: "Paying Customer"}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	if rate := e.Do(t, http.MethodPost, "/v1/apps/stripe-export/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &minute}, nil); rate.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", rate.Code, rate.Body)
	}
	if _, err := (*e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: (*e.Acct()).ID,
		AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 3, BillableUnits: 3}); err != nil {
		t.Fatal(err)
	}
	statements := "/v1/apps/stripe-export/consumers/" + consumer.ID + "/usage-statements"
	end := minute.Add(time.Hour)
	res = e.Do(t, http.MethodPost, statements, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}, nil)
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &statement); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("statement: %d %s", res.Code, res.Body)
	}
	exportPath := statements + "/" + statement.ID + "/invoice"
	if res := e.Do(t, http.MethodPost, exportPath, struct{}{}, nil); res.Code != http.StatusConflict {
		t.Fatalf("draft export: %d %s, want 409", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, statements+"/"+statement.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", res.Code, res.Body)
	}

	stripe.failNextAdd = true
	if res := e.Do(t, http.MethodPost, exportPath, struct{}{}, nil); res.Code != http.StatusBadGateway {
		t.Fatalf("export with a Stripe failure: %d %s, want 502", res.Code, res.Body)
	}
	res = e.Do(t, http.MethodPost, exportPath, struct{}{}, nil)
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
	if res := e.Do(t, http.MethodPost, exportPath, struct{}{}, nil); res.Code != http.StatusOK || stripe.invoices != 1 || len(stripe.items["in_1"]) != 1 {
		t.Fatalf("replay: %d, invoices=%d items=%v", res.Code, stripe.invoices, stripe.items)
	}
	handoff := e.Do(t, http.MethodGet, statements+"/"+statement.ID+"/handoff", nil, nil)
	if handoff.Code != http.StatusOK || !strings.Contains(handoff.Body.String(), `"in_1"`) {
		t.Fatalf("handoff: %d %s", handoff.Code, handoff.Body)
	}
	if res := e.Do(t, http.MethodGet, exportPath, nil, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"completed"`) {
		t.Fatalf("get export: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodDelete, "/v1/account/billing-connections/stripe", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", res.Code, res.Body)
	}
}

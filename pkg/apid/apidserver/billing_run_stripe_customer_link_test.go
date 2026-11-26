package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBillingRunLinksExistingStripeCustomer(t *testing.T) {
	f := newBillingRunFixture(t)
	readyStripe := "cus_Verified"
	// The Stripe fixture serves one real-mode result to test fail-closed
	// mode checking even if a provider response supplied the requested ID.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/account":
			id := "acct_Original"
			if strings.HasSuffix(r.Header.Get("Authorization"), "OTHER") {
				id = "acct_Other"
			}
			_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
		case "GET /v1/customers/cus_WrongMode":
			_, _ = fmt.Fprint(w, `{"id":"cus_WrongMode","livemode":true}`)
		case "GET /v1/customers/cus_Verified":
			_, _ = fmt.Fprintf(w, `{"id":%q,"livemode":false}`, readyStripe)
		default:
			f.stripe.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	(*f.e.S()).StatementInvoices().Stripe = &stripeinvoice.Client{BaseURL: provider.URL, HTTP: provider.Client()}
	const slug = "stripe-customer-link"
	apidserver.MustSeedApp(t, f.e, slug)
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 25_000, EffectiveFrom: &f.month,
	}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	consumer := f.consumer(t, slug, "crm-1004", 4, "")
	start := func(month time.Time) string {
		t.Helper()
		finalize := false
		run := f.start(t, api.CreateBillingRunRequest{Month: month.Format("2006-01"), Finalize: &finalize})
		f.drain(t, run.ID)
		return "/v1/account/billing-runs/" + run.ID + "/draft-exports"
	}
	review := func(path string) (api.BillingRunDraftExportRow, api.BillingRunDraftExportReview) {
		t.Helper()
		res := f.e.Do(t, http.MethodGet, path, nil, nil)
		var out api.BillingRunDraftExportReview
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusOK ||
			len(out.Items) != 1 || out.Livemode == nil || *out.Livemode || out.ConnectionUpdatedAt == nil {
			t.Fatalf("review: %d %s", res.Code, res.Body)
		}
		return out.Items[0], out
	}
	path := start(f.month)
	row, snapshot := review(path)
	if row.Status != "blocked" || row.BlockerCode != api.CodeStripeCustomerMissing {
		t.Fatalf("missing link row: %+v", row)
	}
	request := api.LinkBillingRunStripeCustomerRequest{
		StripeCustomer: "cus_WrongMode", StatementID: row.StatementID,
		Livemode: snapshot.Livemode, ConnectionUpdatedAt: *snapshot.ConnectionUpdatedAt,
	}
	link := func(req api.LinkBillingRunStripeCustomerRequest, want int) {
		t.Helper()
		res := f.e.Do(t, http.MethodPut, path+"/"+consumer.ID+"/stripe-customer", req, nil)
		if res.Code != want {
			t.Fatalf("link %s: %d %s, want %d", req.StripeCustomer, res.Code, res.Body, want)
		}
	}
	link(request, http.StatusUnprocessableEntity)
	if row, _ := review(path); row.BlockerCode != api.CodeStripeCustomerMissing {
		t.Fatalf("wrong-mode link became visible: %+v", row)
	}
	request.StripeCustomer = readyStripe
	stale := request
	stale.ConnectionUpdatedAt = stale.ConnectionUpdatedAt.Add(-time.Second)
	link(stale, http.StatusConflict)
	link(request, http.StatusNoContent)
	link(request, http.StatusNoContent)
	other := request
	other.StripeCustomer = "cus_OtherCustomer"
	link(other, http.StatusConflict)
	row, snapshot = review(path)
	if row.Status != "ready" || row.StripeCustomer != readyStripe || row.AmountDueMillicents == nil || f.stripe.invoices != 0 {
		t.Fatalf("linked row: %+v, Stripe invoices: %d", row, f.stripe.invoices)
	}
	approval := api.ApproveBillingRunDraftExportRequest{
		ConsumerID: consumer.ID, StatementID: row.StatementID, Revision: row.Revision, StripeCustomer: readyStripe,
		Currency: row.Currency, AmountDueMillicents: *row.AmountDueMillicents,
		Livemode: snapshot.Livemode, ConnectionUpdatedAt: *snapshot.ConnectionUpdatedAt,
	}
	for range 2 {
		res := f.e.Do(t, http.MethodPost, path, approval, nil)
		var invoice api.StatementInvoiceExportResponse
		if err := json.Unmarshal(res.Body.Bytes(), &invoice); err != nil || res.Code != http.StatusOK ||
			invoice.Collection != "none" || invoice.Customer != readyStripe || invoice.InvoiceID != "in_1" {
			t.Fatalf("approved draft: %d %s", res.Code, res.Body)
		}
	}
	if f.stripe.invoices != 1 || len(f.stripe.finalized) != 0 {
		t.Fatalf("invoices=%d, Stripe finalizations=%v", f.stripe.invoices, f.stripe.finalized)
	}
	ctx := context.Background()
	acct := (*f.e.Acct()).ID
	store := *f.e.Store()
	existing, err := store.GetAPIConsumerByID(ctx, acct, consumer.ID)
	if err != nil || existing.ExternalRef != "crm-1004" {
		t.Fatalf("consumer identity: %+v %v", existing, err)
	}
	nextMonth := f.month.AddDate(0, 1, 0)
	if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: acct, AppID: consumer.AppID, ConsumerKey: consumer.ID,
		WindowStart: nextMonth.Add(time.Hour), RequestCount: 4, BillableUnits: 4,
	}); err != nil {
		t.Fatal(err)
	}
	(*f.e.S()).BillingRuns().Now = func() time.Time { return nextMonth.AddDate(0, 1, 1) }
	nextPath := start(nextMonth)
	rotate := func(key string) {
		t.Helper()
		res := f.e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: key}, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("rotate key: %d %s", res.Code, res.Body)
		}
	}
	rotate("rk_test_rotationNEXT")
	if row, _ := review(nextPath); row.Status != "ready" || row.StripeCustomer != readyStripe {
		t.Fatalf("next month after same-account key rotation: %+v", row)
	}
	rotate("rk_test_rotationOTHER")
	if row, _ := review(nextPath); row.Status != "blocked" || row.BlockerCode != api.CodeStripeCustomerMissing {
		t.Fatalf("link leaked to another Stripe account: %+v", row)
	}
	if servedPaths(t, f.e)["/v1/account/billing-runs/{id}/draft-exports/{consumer_id}/stripe-customer"] {
		t.Fatal("customer link route escaped the internal OpenAPI filter")
	}
}

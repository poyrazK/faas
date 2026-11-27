package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBillingRunReviewedStripeDraftExports(t *testing.T) {
	f := newBillingRunFixture(t)
	ctx := context.Background()
	acct := (*f.e.Acct()).ID
	store := *f.e.Store()
	settledHour := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)
	f.month = time.Date(settledHour.Year(), settledHour.Month(), 1, 0, 0, 0, 0, time.UTC)
	(*f.e.S()).BillingRuns().Now = func() time.Time { return f.month.AddDate(0, 1, 1) }
	var calls, collections atomic.Int64
	f.stripe.trackDrafts = true
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/finalize") || strings.HasSuffix(r.URL.Path, "/send") || strings.HasSuffix(r.URL.Path, "/pay") {
			collections.Add(1)
		}
		f.stripe.ServeHTTP(w, r)
	}))
	t.Cleanup(provider.Close)
	(*f.e.S()).StatementInvoices().Stripe = &stripeinvoice.Client{BaseURL: provider.URL, HTTP: provider.Client()}

	const slug = "reviewed-stripe-drafts"
	apidserver.MustSeedApp(t, f.e, slug)
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "USD", PriceMillicentsPerUnit: 25_000, EffectiveFrom: &f.month,
	}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	ready := []api.APIConsumerResponse{
		f.consumer(t, slug, "cus_Ready1", 4, ""),
		f.consumer(t, slug, "cus_Ready2", 4, ""),
		f.consumer(t, slug, "cus_Ready3", 4, ""),
	}
	gap := f.consumer(t, slug, "cus_Held", 4, "")
	unlinked := f.consumer(t, slug, "unlinked", 4, "")
	store.SeedAPIConsumerTelemetryHour(acct, gap.AppID, gap.ID, settledHour, 7)
	finalize := false
	run := f.start(t, api.CreateBillingRunRequest{Month: f.month.Format("2006-01"), Finalize: &finalize})
	path := "/v1/account/billing-runs/" + run.ID + "/draft-exports"
	if res := f.e.Do(t, http.MethodGet, path, nil, nil); res.Code != http.StatusConflict {
		t.Fatalf("review queued run: %d %s", res.Code, res.Body)
	}
	f.drain(t, run.ID)
	read := func() (api.BillingRunDraftExportReview, map[string]api.BillingRunDraftExportRow) {
		t.Helper()
		res := f.e.Do(t, http.MethodGet, path, nil, nil)
		var review api.BillingRunDraftExportReview
		if err := json.Unmarshal(res.Body.Bytes(), &review); err != nil || res.Code != http.StatusOK ||
			len(review.Items) != 5 || review.Livemode == nil || *review.Livemode || review.ConnectionUpdatedAt == nil {
			t.Fatalf("review: %d %s", res.Code, res.Body)
		}
		rows := make(map[string]api.BillingRunDraftExportRow)
		for _, item := range review.Items {
			rows[item.ConsumerID] = item
		}
		return review, rows
	}
	review, rows := read()
	if calls.Load() != 0 || rows[gap.ID].Status != "blocked" || !strings.Contains(rows[gap.ID].Reason, "usage ledger") ||
		rows[unlinked.ID].Status != "blocked" || !strings.Contains(rows[unlinked.ID].Reason, "Stripe customer") {
		t.Fatalf("unexpected preflight: %+v, Stripe calls %d", rows, calls.Load())
	}
	for _, customer := range ready {
		row := rows[customer.ID]
		if row.Status != "ready" || row.AmountDueMillicents == nil || *row.AmountDueMillicents != 100_000 {
			t.Fatalf("ready row: %+v", row)
		}
	}

	approve := func(req api.ApproveBillingRunDraftExportRequest, want int) api.StatementInvoiceExportResponse {
		t.Helper()
		res := f.e.Do(t, http.MethodPost, path, req, nil)
		if res.Code != want {
			t.Fatalf("approve %s: %d %s, want %d", req.ConsumerID, res.Code, res.Body, want)
		}
		var invoice api.StatementInvoiceExportResponse
		if want == http.StatusOK {
			if err := json.Unmarshal(res.Body.Bytes(), &invoice); err != nil || invoice.InvoiceID == "" ||
				invoice.Collection != "none" || invoice.Status != "completed" || invoice.Livemode {
				t.Fatalf("invoice: %s, %v", res.Body, err)
			}
		}
		return invoice
	}
	request := func(consumer api.APIConsumerResponse) api.ApproveBillingRunDraftExportRequest {
		row := rows[consumer.ID]
		return api.ApproveBillingRunDraftExportRequest{
			ConsumerID: consumer.ID, StatementID: row.StatementID, Revision: row.Revision,
			Currency: "USD", AmountDueMillicents: 100_000, StripeCustomer: consumer.ExternalRef,
			Livemode: review.Livemode, ConnectionUpdatedAt: *review.ConnectionUpdatedAt,
		}
	}
	for _, customer := range []api.APIConsumerResponse{gap, unlinked} {
		approve(request(customer), http.StatusConflict)
	}
	foreign := request(ready[0])
	foreign.StatementID = uuid.NewString()
	approve(foreign, http.StatusConflict)
	wrongVersion := request(ready[0])
	wrongVersion.ConnectionUpdatedAt = wrongVersion.ConnectionUpdatedAt.Add(-time.Second)
	approve(wrongVersion, http.StatusConflict)

	first := ready[0]
	credit, err := store.CreateAPIConsumerCreditGrant(ctx, state.APIConsumerCreditGrant{
		AccountID: acct, AppID: first.AppID, ConsumerID: first.ID, Currency: "USD", AmountMillicents: 25_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	approve(request(first), http.StatusConflict)
	if statement, err := store.GetAPIConsumerUsageStatement(ctx, acct, first.AppID, first.ID, rows[first.ID].StatementID); err != nil ||
		statement.Status != state.APIConsumerUsageStatementDraft || calls.Load() != 0 {
		t.Fatalf("changed credit caused a side effect: %+v, %v, %d calls", statement, err, calls.Load())
	}
	_, updatedRows := read()
	if row := updatedRows[first.ID]; row.AmountDueMillicents == nil || *row.AmountDueMillicents != 75_000 {
		t.Fatalf("credit-adjusted review: %+v", row)
	}
	if _, err := store.VoidAPIConsumerCreditGrant(ctx, acct, first.AppID, first.ID, credit.ID); err != nil {
		t.Fatal(err)
	}

	f.stripe.failNextAdd = true
	approve(request(first), http.StatusBadGateway)
	second := approve(request(ready[1]), http.StatusOK)
	third := approve(request(ready[2]), http.StatusOK)
	recovered := approve(request(first), http.StatusOK)
	if second.InvoiceID == third.InvoiceID || recovered.InvoiceID != "in_1" ||
		approve(request(first), http.StatusOK).InvoiceID != recovered.InvoiceID {
		t.Fatalf("distinct batches did not resume their original invoices: %s %s %s", recovered.InvoiceID, second.InvoiceID, third.InvoiceID)
	}
	if f.stripe.invoices != 3 || collections.Load() != 0 {
		t.Fatalf("Stripe created %d invoices and received %d collection requests", f.stripe.invoices, collections.Load())
	}
	_, after := read()
	for _, customer := range ready {
		if after[customer.ID].Status != "exported" || after[customer.ID].Invoice == nil {
			t.Fatalf("receipt missing after reload: %+v", after[customer.ID])
		}
	}
	for _, customer := range []api.APIConsumerResponse{gap, unlinked} {
		statement, err := store.GetAPIConsumerUsageStatement(ctx, acct, customer.AppID, customer.ID, rows[customer.ID].StatementID)
		if err != nil || statement.Status != state.APIConsumerUsageStatementDraft {
			t.Fatalf("blocked statement was modified: %+v, %v", statement, err)
		}
	}
	if servedPaths(t, f.e)["/v1/account/billing-runs/{id}/draft-exports"] {
		t.Fatal("draft exports escaped the internal OpenAPI filter")
	}

	totalsPath := "/v1/account/billing-runs/" + run.ID + "/collections"
	totals := func() []api.BillingRunCollectionTotal {
		t.Helper()
		res := f.e.Do(t, http.MethodGet, totalsPath, nil, nil)
		var result api.BillingRunCollectionsResponse
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != http.StatusOK || result.Totals == nil {
			t.Fatalf("collections: %d %s, err=%v", res.Code, res.Body, err)
		}
		return result.Totals
	}
	before := calls.Load()
	if got := totals(); len(got) != 0 || calls.Load() != before {
		t.Fatalf("drafts changed totals or read Stripe: %+v", got)
	}
	due := f.month.AddDate(0, 1, 0)
	paidBeforeDue := due.Add(-time.Hour)
	for _, item := range []struct {
		invoice api.StatementInvoiceExportResponse
		payment state.StatementInvoicePayment
	}{
		{recovered, state.StatementInvoicePayment{Status: state.InvoicePaymentOpen, AmountPaidMinor: 20, AmountRemainingMinor: 80, DueAt: &due}},
		{second, state.StatementInvoicePayment{Status: state.InvoicePaymentPaid, AmountPaidMinor: 100, DueAt: &due, PaidAt: &paidBeforeDue}},
	} {
		export, err := store.GetStatementInvoiceExport(ctx, acct, state.StatementSourceAppConsumer, item.invoice.StatementID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordStatementInvoicePayment(ctx, export.ID, item.payment, due, nil); err != nil {
			t.Fatal(err)
		}
	}
	expected := api.BillingRunCollectionTotal{Currency: "USD", AmountPaidMinor: 120, AmountOutstandingMinor: 80,
		AmountOverdueMinor: 80, InvoicesDue: 2, PaidByDueDate: 1}
	if got := totals(); len(got) != 1 || got[0] != expected || calls.Load() != before {
		t.Fatalf("totals before refresh: %+v, want %+v", got, expected)
	}
	f.stripe.setPayment(recovered.InvoiceID, fmt.Sprintf(
		`{"id":%q,"customer":%q,"livemode":false,"currency":"usd","status":"paid","amount_paid":100,"amount_remaining":0,"due_date":%d,"status_transitions":{"paid_at":%d}}`,
		recovered.InvoiceID, recovered.Customer, due.Unix(), due.Add(time.Hour).Unix()))
	refreshPath := "/v1/account/statement-invoices/" + recovered.StatementID + "/refresh"
	res := f.e.Do(t, http.MethodPost, refreshPath, nil, nil)
	var refreshed api.StatementInvoiceExportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &refreshed); err != nil || res.Code != http.StatusOK ||
		refreshed.PaymentStatus != state.InvoicePaymentPaid || refreshed.InvoiceID != recovered.InvoiceID || refreshed.StatusSyncedAt == nil {
		t.Fatalf("refresh: %d %s, err=%v", res.Code, res.Body, err)
	}
	expected.AmountPaidMinor, expected.AmountOutstandingMinor, expected.AmountOverdueMinor = 200, 0, 0
	if got := totals(); len(got) != 1 || got[0] != expected || calls.Load() != before+1 || collections.Load() != 0 {
		t.Fatalf("totals after refresh: %+v, want %+v", got, expected)
	}
	served := servedPaths(t, f.e)
	if served["/v1/account/billing-runs/{id}/collections"] || served["/v1/account/statement-invoices/{statement_id}/refresh"] {
		t.Fatal("collections routes escaped the internal OpenAPI filter")
	}
}

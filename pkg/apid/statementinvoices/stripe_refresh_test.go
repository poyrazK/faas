package statementinvoices

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStripePaymentRefreshPreservesLastConfirmedBalance(t *testing.T) {
	f := newIssueFixture(t)
	ctx := context.Background()
	export, err := f.store.GetStatementInvoiceExport(ctx, f.account.ID, state.StatementSourceAppConsumer, f.statement)
	if err != nil {
		t.Fatal(err)
	}
	checked := f.now.Add(-time.Hour)
	if _, err := f.store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{
		Status: state.InvoicePaymentOpen, AmountRemainingMinor: 1500,
	}, checked, &f.now); err != nil {
		t.Fatal(err)
	}
	request := func(acct state.Account, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/account/statement-invoices/"+f.statement+"/refresh", nil)
		req.SetPathValue("statement_id", f.statement)
		w := httptest.NewRecorder()
		f.service.RefreshStripePayment(w, req, acct)
		if w.Code != want {
			t.Fatalf("refresh status %d, want %d: %s", w.Code, want, w.Body)
		}
		return w
	}
	request(state.Account{ID: uuid.NewString()}, http.StatusNotFound)
	token := uuid.NewString()
	if _, err := f.store.ClaimStatementInvoiceExport(ctx, f.account.ID, export.ID, token, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	request(f.account, http.StatusConflict)
	if err := f.service.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if f.stripe.requests != 0 {
		t.Fatal("an unrelated or already claimed invoice was read from Stripe")
	}
	if err := f.store.ReleaseStatementInvoiceExport(ctx, f.account.ID, export.ID, token); err != nil {
		t.Fatal(err)
	}

	available := f.service.Stripe
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "try again", http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	f.service.Stripe = &stripeinvoice.Client{BaseURL: unavailable.URL, HTTP: unavailable.Client()}
	request(f.account, http.StatusBadGateway)
	f.service.Stripe = available

	f.stripe.mu.Lock()
	f.stripe.doc["livemode"] = true
	f.stripe.mu.Unlock()
	request(f.account, http.StatusConflict)
	unchanged, err := f.store.GetStatementInvoiceExport(ctx, f.account.ID, state.StatementSourceAppConsumer, f.statement)
	if err != nil || unchanged.PaymentStatus != state.InvoicePaymentOpen || unchanged.AmountRemainingMinor == nil ||
		*unchanged.AmountRemainingMinor != 1500 || unchanged.StatusSyncedAt == nil || !unchanged.StatusSyncedAt.Equal(checked) {
		t.Fatalf("unconfirmed refresh changed stored balance: %+v, err=%v", unchanged, err)
	}

	f.stripe.mu.Lock()
	f.stripe.doc["livemode"], f.stripe.doc["status"] = false, "paid"
	f.stripe.doc["amount_paid"], f.stripe.doc["amount_remaining"] = 1500, 0
	f.stripe.doc["status_transitions"] = map[string]int64{"paid_at": f.now.Unix()}
	f.stripe.mu.Unlock()
	w := request(f.account, http.StatusOK)
	var invoice api.StatementInvoiceExportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &invoice); err != nil || invoice.InvoiceID != issueInvoiceID ||
		invoice.PaymentStatus != state.InvoicePaymentPaid || invoice.AmountPaidMinor == nil || *invoice.AmountPaidMinor != 1500 ||
		invoice.AmountRemainingMinor == nil || *invoice.AmountRemainingMinor != 0 ||
		invoice.PaidAt == nil || !invoice.PaidAt.Equal(f.now) || invoice.StatusSyncedAt == nil || !invoice.StatusSyncedAt.Equal(f.now) {
		t.Fatalf("refresh receipt: %s, err=%v", w.Body, err)
	}
	if f.stripe.writes != 0 {
		t.Fatal("payment check wrote to Stripe")
	}
}

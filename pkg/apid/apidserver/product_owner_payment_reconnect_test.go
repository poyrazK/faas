package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerPaymentSyncAfterReconnect(t *testing.T) {
	fixture := newStripeOwnerFixture(t)
	fixture.now = fixture.now.Add(2 * time.Hour)
	ctx := t.Context()
	scope, live := fixture.draftInMode(t, "Live balance workspace", "cus_live", "in_live", "live@example.com", fixture.month, true)
	_, draft := fixture.draft(t, "Test draft workspace", "cus_test_draft", "in_test_draft", "test@example.com", fixture.month)
	due, observed, next := fixture.now.AddDate(0, 0, -2), fixture.now.Add(-time.Hour), fixture.now
	const hosted = "https://invoice.stripe.com/i/in_live"
	if _, err := fixture.store.RecordStatementInvoicePayment(ctx, live.export, state.StatementInvoicePayment{
		Status: state.InvoicePaymentOpen, AmountRemainingMinor: 900, DueAt: &due, HostedInvoiceURL: hosted,
	}, observed, &next); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RecordStatementInvoicePayment(ctx, draft.export, state.StatementInvoicePayment{
		Status: state.InvoicePaymentDraft, AmountRemainingMinor: 900,
	}, observed, &next); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PutProductOverduePolicy(ctx, state.ProductOverduePolicy{AccountID: scope.AccountID, AppID: scope.AppID}); err != nil {
		t.Fatal(err)
	}

	const liveKey, missingKey = "rk_live_stripe_original", "rk_live_stripe_other"
	var liveReads, draftReads, writes atomic.Int32
	dashboard := fixture.dashboard(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "unexpected provider write", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v1/invoices":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/v1/invoices/" + draft.invoiceID:
			draftReads.Add(1)
			http.NotFound(w, r)
		case "/v1/invoices/" + live.invoiceID:
			liveReads.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+liveKey {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": live.invoiceID, "status": "paid", "currency": "eur", "livemode": true,
				"amount_paid": 900, "amount_remaining": 0, "due_date": due.Unix(), "hosted_invoice_url": hosted,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	dashboard.connectStripe(t, fixture.owner, liveKey)
	dashboard.connectStripe(t, fixture.owner, "rk_test_stripe_reviewed")
	mailer := &apidserver.RecordingMailer{}
	*dashboard.server.Mailer() = mailer
	service := dashboard.server.StatementInvoices()
	sync := func() {
		t.Helper()
		if err := service.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertHold := func(want int) {
		t.Helper()
		if err := dashboard.server.ProductControl().SyncOverdueOnce(ctx); err != nil {
			t.Fatal(err)
		}
		holds, err := fixture.store.ListProductOverdueHolds(ctx, scope.AccountID, scope.AppID)
		if err != nil || len(holds) != want {
			t.Fatalf("overdue holds = %+v, %v, want %d", holds, err, want)
		}
	}
	load := func(statementID string) state.StatementInvoiceExport {
		t.Helper()
		export, err := fixture.store.GetStatementInvoiceExport(ctx, scope.AccountID, state.StatementSourceProductControl, statementID)
		if err != nil {
			t.Fatal(err)
		}
		return export
	}
	assertPreserved := func(retry time.Time) {
		t.Helper()
		export := load(live.statement)
		if export.PaymentStatus != state.InvoicePaymentOpen || export.AmountRemainingMinor == nil || *export.AmountRemainingMinor != 900 ||
			export.StatusSyncedAt == nil || !export.StatusSyncedAt.Equal(observed) || export.HostedInvoiceURL != hosted ||
			export.NextStatusSyncAt == nil || !export.NextStatusSyncAt.Equal(retry) {
			t.Fatalf("expected last confirmed open balance and pending retry, got %+v", export)
		}
		assertHold(1)
	}
	const group = "/outstanding-balances?bucket=overdue&currency=EUR"
	callRefresh := func(status int, guidance string) {
		t.Helper()
		body := dashboard.call(t, group, nil, http.StatusOK, "Live balance workspace", "EUR 9.00")
		form := ownerConsoleForm(t, body, dashboard.base+"/payments/refresh#outstanding-balances")
		dashboard.call(t, "/payments/refresh", form, status, guidance, "Live balance workspace", "EUR 9.00")
	}

	assertHold(1)
	sync()
	assertPreserved(fixture.now.Add(api.StatementInvoiceSyncKeyRetry))
	callRefresh(http.StatusConflict, "Reconnect Stripe in live mode")
	if liveReads.Load() != 0 || draftReads.Load() != 1 {
		t.Fatalf("reads after mode mismatch: live %d, test %d", liveReads.Load(), draftReads.Load())
	}
	if export := load(draft.statement); export.PaymentStatus != state.InvoicePaymentDeleted {
		t.Fatalf("confirmed draft on the matching connection = %+v", export)
	}
	found := false
	for _, msg := range mailer.Snapshot() {
		if strings.Contains(msg.TextBody, "Reconnect Stripe in live mode") {
			found = true
		}
	}
	if !found {
		t.Fatal("sync blocked notification omitted the required connection mode")
	}

	dashboard.connectStripe(t, fixture.owner, missingKey)
	fixture.now = fixture.now.Add(api.ProductOverdueHeldSyncInterval + time.Minute)
	sync()
	assertPreserved(fixture.now.Add(api.StatementInvoiceSyncRetry))
	callRefresh(http.StatusBadGateway, "Stripe could not find this invoice")
	if liveReads.Load() != 2 {
		t.Fatalf("expected a background and manual lookup on the matching mode, got %d", liveReads.Load())
	}

	dashboard.connectStripe(t, fixture.owner, liveKey)
	fixture.now = fixture.now.Add(api.StatementInvoiceSyncRetry)
	// The failed manual refresh keeps its short claim. Advance the store's
	// claim clock as well as the worker clock to let that lease expire.
	claimTime := time.Now().Add(api.StatementInvoiceSyncRetry)
	fixture.store.SetClockForTest(func() time.Time { return claimTime })
	sync()
	export := load(live.statement)
	if export.PaymentStatus != state.InvoicePaymentPaid || export.AmountRemainingMinor == nil || *export.AmountRemainingMinor != 0 ||
		export.NextStatusSyncAt != nil || liveReads.Load() != 3 || writes.Load() != 0 {
		t.Fatalf("recovered export = %+v, provider reads = %d, writes = %d", export, liveReads.Load(), writes.Load())
	}
	assertHold(0)
	dashboard.call(t, group, nil, http.StatusOK, "No outstanding live invoices.")
}

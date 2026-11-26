package statementinvoices

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLookupStripeDraftDeletion(t *testing.T) {
	created := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	now := created.Add(time.Hour)
	for _, tc := range []struct {
		name, status, eventInvoice string
		eventMode, wasSynced       bool
		eventStatus                int
		wantDeleted, wantEventRead bool
	}{
		{name: "deletion before the first sync", status: state.InvoicePaymentDraft, eventInvoice: "in_draft", eventMode: true, wantDeleted: true, wantEventRead: true},
		{name: "another invoice", status: state.InvoicePaymentDraft, eventInvoice: "in_other", eventMode: true, wantEventRead: true},
		{name: "cached draft observation after reconnect", status: state.InvoicePaymentDraft, eventInvoice: "in_other", eventMode: true, wasSynced: true, wantEventRead: true},
		{name: "verified deletion of a previously synced draft", status: state.InvoicePaymentDraft, eventInvoice: "in_draft", eventMode: true, wasSynced: true, wantDeleted: true, wantEventRead: true},
		{name: "other mode", status: state.InvoicePaymentDraft, eventInvoice: "in_draft", wantEventRead: true},
		{name: "events permission missing", status: state.InvoicePaymentDraft, eventStatus: http.StatusForbidden, wantEventRead: true},
		{name: "issued balance", status: state.InvoicePaymentOpen, eventInvoice: "in_draft", eventMode: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var eventReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/invoices/in_draft":
					w.WriteHeader(http.StatusNotFound)
				case "/v1/events":
					eventReads.Add(1)
					query := r.URL.Query()
					if query.Get("type") != "invoice.deleted" || query.Get("created[gte]") != fmt.Sprint(created.Unix()) ||
						query.Get("created[lte]") != fmt.Sprint(now.Unix()) {
						t.Errorf("unexpected event query %s", r.URL.RawQuery)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if tc.eventStatus != 0 {
						w.WriteHeader(tc.eventStatus)
						return
					}
					_, _ = fmt.Fprintf(w, `{"data":[{"id":"evt_deleted","type":"invoice.deleted","created":%d,"livemode":%t,"data":{"object":{"object":"invoice","id":%q}}}]}`,
						created.Add(time.Minute).Unix(), tc.eventMode, tc.eventInvoice)
				default:
					t.Errorf("unexpected Stripe request %s", r.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()

			p := stripeProvider{client: &stripeinvoice.Client{BaseURL: server.URL, HTTP: server.Client()}}
			export := state.StatementInvoiceExport{ProviderInvoiceID: "in_draft", Livemode: true, PaymentStatus: tc.status, CreatedAt: created}
			rotation := now.Add(-5 * time.Minute)
			if tc.wasSynced {
				// A sync tick can still use its cached old key after a rotation.
				observed := rotation.Add(time.Minute)
				export.StatusSyncedAt = &observed
			}
			conn := connection{key: "rk_live_test", livemode: true, updatedAt: rotation}
			payment, known, err := lookupInvoicePayment(t.Context(), p, conn, export, now)
			if tc.wantDeleted {
				if err != nil || !known || payment.Status != state.InvoicePaymentDeleted {
					t.Fatalf("verified deletion returned %+v, known=%t, err=%v", payment, known, err)
				}
			} else if !errors.Is(err, errInvoiceLookupUnconfirmed) || known {
				t.Fatalf("unconfirmed deletion returned %+v, known=%t, err=%v", payment, known, err)
			}
			wantReads := int32(0)
			if tc.wantEventRead {
				wantReads = 1
			}
			if got := eventReads.Load(); got != wantReads {
				t.Fatalf("event reads = %d, want %d", got, wantReads)
			}
		})
	}
}

func TestPaddleCheckoutLinkRequiresIssuance(t *testing.T) {
	const checkoutURL = "https://checkout.example.com/pay"
	for _, tc := range []struct {
		status, wantURL string
	}{
		{status: "draft"},
		{status: "ready"},
		{status: "canceled"},
		{status: "billed", wantURL: checkoutURL},
		{status: "past_due", wantURL: checkoutURL},
		{status: "paid", wantURL: checkoutURL},
	} {
		t.Run(tc.status, func(t *testing.T) {
			payment, known, err := paddleTransactionPayment(paddleinvoice.TransactionState{Status: tc.status, CheckoutURL: checkoutURL})
			if err != nil || !known || payment.HostedInvoiceURL != tc.wantURL {
				t.Fatalf("observed checkout URL = %q, want %q, known=%t, err=%v", payment.HostedInvoiceURL, tc.wantURL, known, err)
			}
			// An existing export may already have an unissued checkout URL.
			response := statementInvoiceExportResponse(state.StatementInvoiceExport{
				Provider: state.BillingProviderPaddle, Status: state.StatementInvoiceExportCompleted,
				PaymentStatus: payment.Status, HostedInvoiceURL: checkoutURL,
			})
			if response.HostedInvoiceURL != tc.wantURL {
				t.Fatalf("existing checkout URL = %q, want %q", response.HostedInvoiceURL, tc.wantURL)
			}
		})
	}
}

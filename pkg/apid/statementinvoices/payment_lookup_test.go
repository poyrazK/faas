package statementinvoices

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
			service := Service{ProductControlEnabled: func() bool { return true }}
			export := state.StatementInvoiceExport{SourceKind: state.StatementSourceProductControl,
				Provider: state.BillingProviderStripe, ProviderInvoiceID: "in_draft", Livemode: true,
				PaymentStatus: tc.status, CreatedAt: created}
			rotation := now.Add(-5 * time.Minute)
			if tc.wasSynced {
				// A sync tick can still use its cached old key after a rotation.
				observed := rotation.Add(time.Minute)
				export.StatusSyncedAt = &observed
			}
			conn := connection{key: "rk_live_test", livemode: true, updatedAt: rotation}
			payment, known, err := service.lookupInvoicePayment(t.Context(), p, conn, export, now)
			if tc.wantDeleted {
				if err != nil || !known || payment.Status != state.InvoicePaymentDeleted {
					t.Fatalf("expected deletion, got %+v, known=%t, err=%v", payment, known, err)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := strings.TrimPrefix(r.URL.Path, "/transactions/")
		_, _ = fmt.Fprintf(w, `{"data":{"status":%q,"checkout":{"url":%q}}}`, status, checkoutURL)
	}))
	defer server.Close()
	p := paddleProvider{client: &paddleinvoice.Client{BaseURL: server.URL, HTTP: server.Client()}}
	for _, tc := range []struct {
		name, status, source, wantURL string
		productControl                bool
	}{
		{name: "Product Control draft", status: "draft", source: state.StatementSourceProductControl, productControl: true},
		{name: "Product Control ready", status: "ready", source: state.StatementSourceProductControl, productControl: true},
		{name: "Product Control canceled", status: "canceled", source: state.StatementSourceProductControl, productControl: true},
		{name: "Product Control billed", status: "billed", source: state.StatementSourceProductControl, productControl: true, wantURL: checkoutURL},
		{name: "Product Control past due", status: "past_due", source: state.StatementSourceProductControl, productControl: true, wantURL: checkoutURL},
		{name: "Product Control paid", status: "paid", source: state.StatementSourceProductControl, productControl: true, wantURL: checkoutURL},
		{name: "flag disabled", status: "ready", source: state.StatementSourceProductControl, wantURL: checkoutURL},
		{name: "app consumer", status: "ready", source: state.StatementSourceAppConsumer, productControl: true, wantURL: checkoutURL},
		{name: "platform tenant", status: "ready", source: state.StatementSourcePlatformTenant, productControl: true, wantURL: checkoutURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Service{ProductControlEnabled: func() bool { return tc.productControl }}
			export := state.StatementInvoiceExport{SourceKind: tc.source, Provider: state.BillingProviderPaddle, ProviderInvoiceID: tc.status,
				Status: state.StatementInvoiceExportCompleted}
			payment, known, err := s.lookupInvoicePayment(t.Context(), p, connection{key: "pdl_sdbx_apikey_fixture"}, export, time.Now())
			if err != nil || !known || payment.HostedInvoiceURL != tc.wantURL {
				t.Fatalf("observed checkout URL = %q, want %q, known=%t, err=%v", payment.HostedInvoiceURL, tc.wantURL, known, err)
			}
			// An existing export may already have an unissued checkout URL.
			export.PaymentStatus, export.HostedInvoiceURL = payment.Status, checkoutURL
			response := s.statementInvoiceExportResponse(export)
			if response.HostedInvoiceURL != tc.wantURL {
				t.Fatalf("existing checkout URL = %q, want %q", response.HostedInvoiceURL, tc.wantURL)
			}
		})
	}
}

func TestInvoiceSyncProductControlGate(t *testing.T) {
	for _, tc := range []struct {
		name, source, wantStatus string
		productControl, wantRead bool
	}{
		{name: "app consumer", source: state.StatementSourceAppConsumer, productControl: true, wantRead: true, wantStatus: state.InvoicePaymentDeleted},
		{name: "platform tenant", source: state.StatementSourcePlatformTenant, productControl: true, wantRead: true, wantStatus: state.InvoicePaymentDeleted},
		{name: "Product Control disabled", source: state.StatementSourceProductControl, wantRead: true, wantStatus: state.InvoicePaymentDeleted},
		{name: "Product Control enabled", source: state.StatementSourceProductControl, productControl: true, wantStatus: state.InvoicePaymentOpen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIssueFixture(t)
			f.service.ProductControlEnabled = func() bool { return tc.productControl }
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.URL.Path != "/v1/invoices/"+issueInvoiceID {
					t.Errorf("unexpected Stripe request %s", r.URL)
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			f.service.Stripe = &stripeinvoice.Client{BaseURL: server.URL, HTTP: server.Client()}
			ctx := t.Context()
			statementID := uuid.NewString()
			export, _, err := f.store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{
				AccountID: f.account.ID, SourceKind: tc.source, StatementID: statementID, Provider: state.BillingProviderStripe,
				Customer: "cus_REVIEWED", Currency: "USD", AmountMinor: 1200, Livemode: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.SetStatementInvoiceExportInvoice(ctx, f.account.ID, export.ID, issueInvoiceID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CompleteStatementInvoiceExport(ctx, f.account.ID, export.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{
				Status: state.InvoicePaymentOpen, AmountRemainingMinor: 1200,
			}, f.now.Add(-time.Minute), &f.now); err != nil {
				t.Fatal(err)
			}
			if err := f.service.SyncOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.GetStatementInvoiceExport(ctx, f.account.ID, tc.source, statementID)
			if err != nil || got.PaymentStatus != tc.wantStatus || (reads.Load() > 0) != tc.wantRead {
				t.Fatalf("invoice status = %q, want %q, attempted provider read = %t, error = %v",
					got.PaymentStatus, tc.wantStatus, reads.Load() > 0, err)
			}
		})
	}
}

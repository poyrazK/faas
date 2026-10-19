package product

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductDocumentInvoiceProjection(t *testing.T) {
	due := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	remaining := int64(50)
	completed := api.StatementInvoiceExportResponse{Provider: "stripe", Customer: "cus_1", Status: state.StatementInvoiceExportCompleted, InvoiceID: "in_1",
		DashboardURL: "https://dashboard.stripe.com/test/invoices/in_1", Currency: "EUR", AmountMinor: 50, PaymentStatus: state.InvoicePaymentOpen,
		AmountRemainingMinor: &remaining, DueAt: &due, HostedInvoiceURL: "https://invoice.stripe.com/i/acct_1/in_1", Overdue: true}
	pending := completed
	pending.Status, pending.InvoiceID, pending.DashboardURL, pending.PaymentStatus, pending.HostedInvoiceURL = state.StatementInvoiceExportCreating, "", "", "", ""
	for _, tc := range []struct {
		name       string
		export     *api.StatementInvoiceExportResponse
		hostedURL  string
		owner      bool
		want       bool
		wantHosted string
	}{
		{"owner sees provider details", &completed, "", true, true, completed.HostedInvoiceURL},
		{"customer sees status and hosted link only", &completed, "", false, true, completed.HostedInvoiceURL},
		{"customer never sees an unfinished export", &pending, "", false, false, ""},
		{"owner sees an unfinished export to retry", &pending, "", true, true, ""},
		{"script link dropped", &completed, "javascript:alert(1)", false, true, ""},
		{"plain http link dropped", &completed, "http://invoice.example/in_1", false, true, ""},
		{"link with credentials dropped", &completed, "https://user:pass@invoice.example/in_1", true, true, ""},
		{"not exported", nil, "", true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			export := tc.export
			if export != nil && tc.hostedURL != "" {
				copied := *export
				copied.HostedInvoiceURL = tc.hostedURL
				export = &copied
			}
			got := documentInvoice(export, tc.owner)
			if (got != nil) != tc.want {
				t.Fatalf("invoice = %+v, want present=%v", got, tc.want)
			}
			if got == nil {
				return
			}
			if got.HostedInvoiceURL != tc.wantHosted || got.PaymentStatus != export.PaymentStatus || got.Overdue != export.Overdue || got.AmountMinor != 50 {
				t.Fatalf("invoice = %+v", got)
			}
			ownerOnly := got.Provider != "" || got.InvoiceID != "" || got.DashboardURL != "" || got.ExportStatus != ""
			if !tc.owner && ownerOnly {
				t.Fatalf("customer invoice leaks owner-only fields: %+v", got)
			}
			if tc.owner && (got.Provider != "stripe" || got.ExportStatus != export.Status || got.InvoiceID != export.InvoiceID) {
				t.Fatalf("owner invoice = %+v", got)
			}
		})
	}
}

type invoiceBillingFixture struct {
	state.ProductBillingStore
	docs     []api.ProductBillingDocument
	handoffs map[string]string
}

func (f *invoiceBillingFixture) ListProductBillingDocuments(context.Context, state.ProductBillingScope, string) (api.ProductBillingDocuments, error) {
	return api.ProductBillingDocuments{Documents: append([]api.ProductBillingDocument(nil), f.docs...)}, nil
}

func (f *invoiceBillingFixture) GetProductBillingDocument(_ context.Context, _ state.ProductBillingScope, id string) (api.ProductBillingDocument, error) {
	for _, doc := range f.docs {
		if doc.ID == id {
			return doc, nil
		}
	}
	return api.ProductBillingDocument{}, state.ErrNotFound
}

func (f *invoiceBillingFixture) GetProductBillingHandoff(_ context.Context, _ state.ProductBillingScope, id string) (api.ProductInvoiceHandoff, error) {
	if ref, ok := f.handoffs[id]; ok {
		return api.ProductInvoiceHandoff{DocumentID: id, ExternalInvoiceID: ref}, nil
	}
	return api.ProductInvoiceHandoff{}, state.ErrNotFound
}

func (f *invoiceBillingFixture) HandoffProductBilling(_ context.Context, _ state.ProductBillingScope, id, ref string) (api.ProductInvoiceHandoff, error) {
	f.handoffs[id] = ref
	return api.ProductInvoiceHandoff{DocumentID: id, ExternalInvoiceID: ref}, nil
}

// adr: 20261018212918 — the owner console shows each exported statement's
// provider invoice and payment status in place of the manual handoff form,
// and a started export refuses a manual handoff.
func TestProductOwnerConsoleShowsStatementInvoices(t *testing.T) {
	account := uuid.NewString()
	paid, overdue, pending, manual := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	due := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	remaining := int64(1250)
	exports := map[string]*api.StatementInvoiceExportResponse{
		paid: {Provider: "paddle", Status: state.StatementInvoiceExportCompleted, InvoiceID: "txn_1", Currency: "EUR", AmountMinor: 50,
			PaymentStatus: state.InvoicePaymentPaid, DashboardURL: "https://sandbox-vendors.paddle.com/transactions-v2/txn_1"},
		overdue: {Provider: "stripe", Status: state.StatementInvoiceExportCompleted, InvoiceID: "in_2", Currency: "EUR", AmountMinor: 1250,
			PaymentStatus: state.InvoicePaymentOpen, Overdue: true, DueAt: &due, AmountRemainingMinor: &remaining,
			HostedInvoiceURL: "https://invoice.stripe.com/i/acct_1/in_2", DashboardURL: "https://dashboard.stripe.com/test/invoices/in_2"},
		pending: {Provider: "lago", Status: state.StatementInvoiceExportCreating, Currency: "EUR", AmountMinor: 50},
	}
	var docs []api.ProductBillingDocument
	for _, id := range []string{paid, overdue, pending, manual} {
		docs = append(docs, api.ProductBillingDocument{ID: id, Kind: "statement", Currency: "EUR", AmountMillicents: 50000, CreatedAt: due})
	}
	billing := &invoiceBillingFixture{docs: docs, handoffs: map[string]string{paid: "txn_1", overdue: "in_2", manual: "internal-7"}}
	s := &Service{DocumentInvoice: func(_ context.Context, accountID, id string) (*api.StatementInvoiceExportResponse, error) {
		if accountID != account {
			t.Fatalf("export lookup crossed accounts: %s", accountID)
		}
		return exports[id], nil
	}}
	c := &ownerContext{account: state.Account{ID: account}, billing: billing, data: dashboard.ProductControlData{AppSlug: "exports", BaseURL: "/dashboard/apps/exports/product-control",
		TenantID: uuid.NewString(), Period: "2026-10", Billing: &api.ProductBillingReview{Period: "2026-10", Currency: "EUR", Closed: true}}}
	if err := s.ownerDocuments(httptest.NewRequest("GET", "/", nil), c, state.ProductBillingScope{AccountID: account}, url.Values{}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Title: "Product Control", Body: "product_control", Data: c.data}); err != nil {
		t.Fatal(err)
	}
	page := w.Body.String()
	for _, want := range []string{
		"Invoiced in Paddle as txn_1 · paid",
		"Invoiced in Stripe as in_2 · overdue · due 2026-11-01 · EUR 12.50 remaining",
		`href="https://invoice.stripe.com/i/acct_1/in_2"`,
		`href="https://dashboard.stripe.com/test/invoices/in_2"`,
		"Export to Lago has not finished. Retry gregale product statements export",
		"Invoice handoff recorded: <code>internal-7</code>",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("console misses %q", want)
		}
	}
	if forms := strings.Count(page, `name="action" value="handoff"`); forms != 0 {
		t.Fatalf("console offers %d manual handoff forms for exported or handed-off statements", forms)
	}

	scope := state.ProductBillingScope{AccountID: account}
	if _, err := s.handoffManually(t.Context(), billing, account, scope, pending, "internal-8"); !errors.Is(err, errDocumentExported) || !errors.Is(err, state.ErrConflict) {
		t.Fatalf("manual handoff of an exporting statement: %v", err)
	}
	if _, ok := billing.handoffs[pending]; ok {
		t.Fatal("refused handoff was recorded")
	}
	if _, err := s.handoffManually(t.Context(), billing, account, scope, uuid.NewString(), "internal-9"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("manual handoff of an unknown document: %v", err)
	}
	fresh := uuid.NewString()
	billing.docs = append(billing.docs, api.ProductBillingDocument{ID: fresh, Kind: "statement", Currency: "EUR"})
	if got, err := s.handoffManually(t.Context(), billing, account, scope, fresh, "internal-10"); err != nil || got.ExternalInvoiceID != "internal-10" {
		t.Fatalf("manual handoff of an unexported statement: %+v %v", got, err)
	}
}

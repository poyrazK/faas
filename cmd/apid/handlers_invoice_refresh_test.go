package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/focus"
	"github.com/onebox-faas/faas/pkg/state"
)

type invoiceRefreshProvider struct {
	fakeBillingProvider
	read func(context.Context, state.Account, state.Invoice) (*state.InvoiceDetails, error)
}

func (p *invoiceRefreshProvider) FetchInvoiceDetails(ctx context.Context, acct state.Account, inv state.Invoice) (*state.InvoiceDetails, error) {
	return p.read(ctx, acct, inv)
}

// adr:383
func TestFOCUSInvoiceRefreshOwnershipAndCoverage(t *testing.T) {
	e := setupWithScopes(t, []string{"usage:read"})
	if err := e.store.UpdateAccountProviderCustomerID(t.Context(), e.acct.ID, "cus_refresh"); err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := e.store.UpsertInvoice(t.Context(), state.Invoice{AccountID: e.acct.ID, Provider: "stripe", ProviderInvoiceID: "in_refresh", ProviderChargeID: "ch_refresh", Status: "paid", Currency: "eur", Plan: api.PlanPro, TotalCents: 1000, AmountPaidCents: 1000, PeriodStart: period, PeriodEnd: period.AddDate(0, 0, 29)}); err != nil {
		t.Fatal(err)
	}
	inv, err := e.store.GetInvoiceByProviderID(t.Context(), e.acct.ID, "stripe", "in_refresh")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := &invoiceRefreshProvider{read: func(ctx context.Context, acct state.Account, input state.Invoice) (*state.InvoiceDetails, error) {
		calls++
		if acct.ID != e.acct.ID || acct.ProviderCustomerID != "cus_refresh" || input.ID != inv.ID {
			t.Fatal("unqualified refresh input")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("refresh has no bounded deadline")
		}
		return &state.InvoiceDetails{PaymentTerms: "Net 30", IssuerName: "Invoice issuer", Lines: &state.InvoiceLines{Complete: true, Items: []state.InvoiceLineItem{{ID: "plan", ChargeCategory: "Purchase", NetCents: 1000}}}}, nil
	}}
	e.s.WithBillingProvider(provider)
	path := "/v1/invoices/" + inv.ID + "/refresh"
	rec := e.do(t, http.MethodPost, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh=%d %s", rec.Code, rec.Body)
	}
	var response api.InvoiceRefreshResponse
	if json.Unmarshal(rec.Body.Bytes(), &response) != nil || !response.Detailed || response.LineItems != 1 || response.InvoiceID != inv.ID {
		t.Fatalf("response=%s", rec.Body)
	}
	first, err := e.store.GetInvoiceByID(t.Context(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.AmountPaidCents != inv.AmountPaidCents || first.Status != inv.Status || first.ProviderChargeID != inv.ProviderChargeID {
		t.Fatal("refresh changed payment facts")
	}
	if rec = e.do(t, http.MethodPost, path, nil, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	second, _ := e.store.GetInvoiceByID(t.Context(), inv.ID)
	if !reflect.DeepEqual(first.Lifecycle, second.Lifecycle) {
		t.Fatal("refresh replay advanced record history")
	}
	rec = e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=metadata", nil, nil)
	var m focus.Metadata
	if json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Projection.SourceCoverage.DetailedInvoices != 1 || m.Projection.SourceCoverage.MissingPaymentTerms != 0 {
		t.Fatal("refresh did not improve exported source coverage")
	}
	// Inactive accounts retain access to billing facts for recovery.
	if err := e.store.UpdateAccountStatus(t.Context(), e.acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if rec = e.do(t, http.MethodPost, path, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("suspended refresh=%d %s", rec.Code, rec.Body)
	}
	if err := e.store.UpdateAccountStatus(t.Context(), e.acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	beforeCalls := calls
	other, err := e.store.CreateAccount(t.Context(), "refresh-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := inv
	foreign.ID, foreign.AccountID, foreign.ProviderInvoiceID = "", other.ID, "in_foreign"
	if err := e.store.UpsertInvoice(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	foreign, _ = e.store.GetInvoiceByProviderID(t.Context(), other.ID, "stripe", "in_foreign")
	assertProblem(t, e.do(t, http.MethodPost, "/v1/invoices/"+foreign.ID+"/refresh", nil, nil), http.StatusNotFound, api.CodeNotFound)
	assertProblem(t, e.do(t, http.MethodPost, path+"?provider=stripe", nil, nil), http.StatusBadRequest, api.CodeValidation)
	assertProblem(t, e.do(t, http.MethodPost, path, map[string]string{"invoice_id": "in_foreign"}, nil), http.StatusBadRequest, api.CodeValidation)
	assertProblem(t, e.do(t, http.MethodPost, "/v1/invoices/not-a-uuid/refresh", nil, nil), http.StatusBadRequest, api.CodeValidation)
	assertProblem(t, e.do(t, http.MethodPost, path, nil, map[string]string{"Authorization": ""}), http.StatusUnauthorized, api.CodeUnauthorized)
	e.s.WithBillingProvider(&polar.Provider{})
	assertProblem(t, e.do(t, http.MethodPost, path, nil, nil), http.StatusConflict, api.CodeConflict)
	e.s.WithBillingProvider(&fakeBillingProvider{})
	assertProblem(t, e.do(t, http.MethodPost, path, nil, nil), http.StatusNotImplemented, api.CodeNotImplemented)
	e.s.WithBillingProvider(provider)
	if calls != beforeCalls {
		t.Fatal("invalid or foreign request reached provider")
	}
	// A webhook which arrives during the provider read wins over stale facts.
	provider.read = func(ctx context.Context, _ state.Account, input state.Invoice) (*state.InvoiceDetails, error) {
		input.Details = &state.InvoiceDetails{PaymentTerms: "New webhook terms"}
		if err := e.store.UpsertInvoice(ctx, input); err != nil {
			t.Fatal(err)
		}
		return &state.InvoiceDetails{PaymentTerms: "Stale refresh terms"}, nil
	}
	assertProblem(t, e.do(t, http.MethodPost, path, nil, nil), http.StatusConflict, api.CodeConflict)
	stored, _ := e.store.GetInvoiceByID(t.Context(), inv.ID)
	if stored.Details.PaymentTerms != "New webhook terms" {
		t.Fatal("stale refresh overwrote webhook")
	}
	provider.read = func(context.Context, state.Account, state.Invoice) (*state.InvoiceDetails, error) {
		return nil, errors.New("provider unavailable")
	}
	assertProblem(t, e.do(t, http.MethodPost, path, nil, nil), http.StatusServiceUnavailable, api.CodeCapacity)
}

// adr:383
func TestFOCUSInvoiceRefreshScopeAndMFA(t *testing.T) {
	e := setupWithScopes(t, []string{"apps:read"})
	assertProblem(t, e.do(t, http.MethodPost, "/v1/invoices/00000000-0000-0000-0000-000000000000/refresh", nil, nil), http.StatusForbidden, api.CodeForbidden)
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	cookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)
	assertProblem(t, cookieDo(t, h, cookie, http.MethodPost, "/v1/invoices/00000000-0000-0000-0000-000000000000/refresh", nil), http.StatusForbidden, api.CodeMFARequired)
	problem := invoiceRefreshProblem(&billing.InvoiceRefreshLimitError{Kind: "line items", Limit: api.MaxInvoiceLineItems, Observed: api.MaxInvoiceLineItems + 25})
	if problem.Status != http.StatusUnprocessableEntity || problem.Limit == nil || *problem.Limit != api.MaxInvoiceLineItems || problem.Observed == nil || *problem.Observed != api.MaxInvoiceLineItems+25 || problem.DocsURL == "" {
		t.Fatalf("wrong refresh limit mapping: %+v", problem)
	}
}

// adr:383
func TestFOCUSInvoiceRefreshLegacyStripeIdentity(t *testing.T) {
	e := setupWithScopes(t, []string{"usage:read"})
	if err := e.store.UpdateAccountProviderCustomerID(t.Context(), e.acct.ID, "ctm_stale_legacy"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: e.acct.ID, Provider: "stripe", CustomerID: "cus_qualified"}); err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := e.store.UpsertInvoice(t.Context(), state.Invoice{AccountID: e.acct.ID, Provider: "stripe", ProviderInvoiceID: "in_legacy_refresh", Status: "paid", Currency: "eur", Plan: api.PlanPro, TotalCents: 1000, PeriodStart: period, PeriodEnd: period.AddDate(0, 0, 29)}); err != nil {
		t.Fatal(err)
	}
	inv, err := e.store.GetInvoiceByProviderID(t.Context(), e.acct.ID, "stripe", "in_legacy_refresh")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.s.WithBillingProviderName("stripe")
	e.s.legacyStripeInvoiceReader = &invoiceRefreshProvider{read: func(_ context.Context, acct state.Account, input state.Invoice) (*state.InvoiceDetails, error) {
		calls++
		if acct.ProviderCustomerID != "cus_qualified" || input.ID != inv.ID {
			t.Fatal("legacy Stripe refresh used unqualified identity")
		}
		return &state.InvoiceDetails{PaymentTerms: "Net 30"}, nil
	}}
	path := "/v1/invoices/" + inv.ID + "/refresh"
	rec := e.do(t, http.MethodPost, path, nil, nil)
	if rec.Code != http.StatusOK || calls != 1 || e.s.billingProvider != nil {
		t.Fatalf("legacy Stripe refresh=%d %s", rec.Code, rec.Body)
	}
	e.s.WithBillingProviderName("paddle")
	assertProblem(t, e.do(t, http.MethodPost, path, nil, nil), http.StatusNotImplemented, api.CodeNotImplemented)
	e.s.WithBillingProviderName("stripe").WithBillingMode(billing.ModeDisabled)
	assertProblem(t, e.do(t, http.MethodPost, path, nil, nil), http.StatusNotImplemented, api.CodeNotImplemented)
	if calls != 1 {
		t.Fatal("inactive reader reached provider")
	}
}

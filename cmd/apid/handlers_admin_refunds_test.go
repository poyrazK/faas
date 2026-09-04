package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

func newAdminRefundProvider(t *testing.T, handler http.Handler) *polar.Provider {
	t.Helper()
	providerServer := httptest.NewServer(handler)
	t.Cleanup(providerServer.Close)
	provider, err := polar.NewProvider(polar.Config{
		APIKey:  "polar_test_token",
		BaseURL: providerServer.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create Polar provider: %v", err)
	}
	return provider
}

func seedPaidPolarInvoice(t *testing.T, e testEnv, status string, accountID string) state.Invoice {
	t.Helper()
	invoice := state.Invoice{
		ID:                "00000000-0000-0000-0000-000000000123",
		AccountID:         accountID,
		Provider:          "polar",
		ProviderInvoiceID: "order-1",
		Status:            status,
		TotalCents:        1000,
		AmountPaidCents:   1000,
		Currency:          "eur",
	}
	e.store.SeedInvoiceForTest(invoice)
	return invoice
}

func TestAdminRefund_HappyPathForwardsIdempotencyAndAudits(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	invoice := seedPaidPolarInvoice(t, e, "paid", e.acct.ID)
	var gotKey string
	var gotBody struct {
		OrderID string `json:"order_id"`
		Amount  int64  `json:"amount"`
		Reason  string `json:"reason"`
	}
	provider := newAdminRefundProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/refunds" {
			t.Errorf("provider request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode provider body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"refund-1","amount":500,"currency":"eur","status":"succeeded"}`)
	}))
	e.s.WithBillingProvider(provider)

	key := "operator-refund-1"
	rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+e.acct.ID+"/refunds", map[string]any{
		"invoice_id": invoice.ID, "amount_cents": 500, "reason": "customer request",
	}, map[string]string{"Idempotency-Key": key})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if gotKey != key {
		t.Errorf("provider Idempotency-Key = %q, want %q", gotKey, key)
	}
	if gotBody.OrderID != invoice.ProviderInvoiceID || gotBody.Amount != 500 || gotBody.Reason != "customer_request" {
		t.Errorf("provider body = %+v", gotBody)
	}
	var resp api.AdminRefundResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body)
	}
	if resp.ProviderRefundID != "refund-1" || resp.InvoiceID != invoice.ID || resp.AmountCents != 500 {
		t.Errorf("response = %+v", resp)
	}
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 50)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	var found bool
	for _, event := range events {
		if event.Kind == "refund.requested" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("refund.requested audit event missing")
	}
}

func TestAdminRefund_RejectsMissingIdempotencyKey(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	seedPaidPolarInvoice(t, e, "paid", e.acct.ID)
	e.s.WithBillingProvider(newAdminRefundProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("provider must not be called without an idempotency key")
		w.WriteHeader(http.StatusInternalServerError)
	})))

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/accounts/"+e.acct.ID+"/refunds", strings.NewReader(`{"invoice_id":"00000000-0000-0000-0000-000000000123","amount_cents":500,"reason":"customer request"}`))
	e.addAdminSession(t, req)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestAdminRefund_BindsInvoiceToAccountAndPaidState(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	other, err := e.store.CreateAccount(context.Background(), "other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create other account: %v", err)
	}
	invoice := seedPaidPolarInvoice(t, e, "paid", other.ID)
	called := false
	e.s.WithBillingProvider(newAdminRefundProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	})))

	rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+e.acct.ID+"/refunds", map[string]any{
		"invoice_id": invoice.ID, "amount_cents": 500, "reason": "customer request",
	}, map[string]string{"Idempotency-Key": "operator-refund-2"})
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
	if called {
		t.Fatal("provider was called for an invoice owned by another account")
	}

	invoice.AccountID = e.acct.ID
	invoice.Status = "open"
	e.store.SeedInvoiceForTest(invoice)
	rec = e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+e.acct.ID+"/refunds", map[string]any{
		"invoice_id": invoice.ID, "amount_cents": 500, "reason": "customer request",
	}, map[string]string{"Idempotency-Key": "operator-refund-3"})
	assertProblem(t, rec, http.StatusConflict, api.CodeConflict)
}

func TestAdminRefund_ProviderFailureIsBadGateway(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	invoice := seedPaidPolarInvoice(t, e, "paid", e.acct.ID)
	e.s.WithBillingProvider(newAdminRefundProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"detail":"refund rejected"}`)
	})))

	rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+e.acct.ID+"/refunds", map[string]any{
		"invoice_id": invoice.ID, "amount_cents": 500, "reason": "customer request",
	}, map[string]string{"Idempotency-Key": "operator-refund-4"})
	assertProblem(t, rec, http.StatusBadGateway, "billing_refund_failed")
	if strings.Contains(rec.Body.String(), "operator-refund-4") {
		t.Error("problem response must not echo the idempotency key")
	}
}

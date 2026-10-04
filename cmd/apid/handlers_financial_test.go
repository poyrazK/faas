package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// adr: 530 — the automated cost surface is read-only and account scoped.
func TestFinancialReadOnlyAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "billing-reader", []string{api.ScopeUsageRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	for _, path := range []string{"/v1/billing/costs", "/v1/billing/forecast"} {
		r := e.do(t, http.MethodGet, path, nil, nil)
		if r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cost API: %d, %s", r.Code, r.Body)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if string(body["currency"]) != `"EUR"` || len(body["missing_bill_components"]) == 0 {
			t.Fatalf("missing financial coverage: %s", r.Body)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		unauthenticated := httptest.NewRecorder()
		e.h.ServeHTTP(unauthenticated, req)
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous cost API: %d", unauthenticated.Code)
		}
		for _, query := range []string{"?month=wrong", "?month=0001-01", "?month=2026-10&month=2026-09", "?account_id=other", "?month=9999-01"} {
			bad := e.do(t, http.MethodGet, path+query, nil, nil)
			if bad.Code != http.StatusBadRequest {
				t.Fatalf("invalid query %s: %d %s", query, bad.Code, bad.Body)
			}
		}
	}
	key, hash, _ = api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "unrelated-reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	r := e.do(t, http.MethodGet, "/v1/billing/costs", nil, nil)
	if r.Code != http.StatusForbidden {
		t.Fatalf("unrelated scope read billing: %d %s", r.Code, r.Body)
	}
}

// adr: 530 — preview is a read permission and never persists stopping intent.
func TestFinancialBudgetPreviewAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "budget-reader", []string{api.ScopeUsageRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	spec := financial.BudgetSpec{Name: "previews", Scope: financial.BudgetScope{Kind: "account"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000, NotifyMillicents: []int64{800}, Mode: "monitored", Action: "stop_previews", DrainSeconds: 30, ResumeRule: "manual", Enabled: true}
	preview := e.do(t, http.MethodPost, "/v1/billing/budgets/preview", api.FinancialBudgetPreviewRequest{Spec: spec}, nil)
	if preview.Code != http.StatusOK || preview.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body)
	}
	var out api.FinancialBudgetPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &out); err != nil || out.EnforcementReady || len(out.Reasons) == 0 {
		t.Fatalf("preview claims activation: %+v %v", out, err)
	}
	rows, err := e.store.ListFinancialBudgets(t.Context(), e.acct.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("preview persisted intent: %+v %v", rows, err)
	}
	for _, tc := range []struct {
		path   string
		spec   financial.BudgetSpec
		status int
	}{
		{"/v1/billing/budgets/preview?month=2026-10", spec, http.StatusBadRequest},
		{"/v1/billing/budgets/preview", financial.BudgetSpec{}, http.StatusBadRequest},
	} {
		bad := e.do(t, http.MethodPost, tc.path, api.FinancialBudgetPreviewRequest{Spec: tc.spec}, nil)
		if bad.Code != tc.status {
			t.Fatalf("invalid preview: %d %s", bad.Code, bad.Body)
		}
	}
	key, hash, _ = api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "unrelated", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	forbidden := e.do(t, http.MethodPost, "/v1/billing/budgets/preview", api.FinancialBudgetPreviewRequest{Spec: spec}, nil)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("unrelated key read financial scope: %d %s", forbidden.Code, forbidden.Body)
	}
}

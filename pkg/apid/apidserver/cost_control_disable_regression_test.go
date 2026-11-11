package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCostControlDisableDeletedAppBudgetThroughAPI(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	store := state.NewMemStore()
	a, err := store.CreateAccount(t.Context(), "disabled-budget-api@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "disabled-budget", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "saved budget", Scope: financial.BudgetScope{Kind: "app", ID: app.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	p, err := store.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), a.ID, hash, "cost-control", []string{api.ScopeAdmin}); err != nil {
		t.Fatal(err)
	}
	spec.Enabled = false
	body, err := json.Marshal(api.UpdateFinancialBudgetRequest{ExpectedRevision: p.Revision, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/billing/budgets/"+p.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "disable-deleted-scope")
	rec := httptest.NewRecorder()
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable endpoint=%d %s", rec.Code, rec.Body)
	}
	var got api.FinancialBudgetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Enabled || got.Revision != p.Revision+1 || got.Status != "draft" {
		t.Fatalf("disabled response=%+v", got)
	}
}

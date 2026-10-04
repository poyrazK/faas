package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func financialDraftSpec() financial.BudgetSpec {
	return financial.BudgetSpec{Name: "Preview spending", Scope: financial.BudgetScope{Kind: "account"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000000, NotifyMillicents: []int64{800000}, Mode: "monitored", Action: "stop_previews", DrainSeconds: 30, ResumeRule: "manual"}
}

func decodeFinancialBudget(t *testing.T, rec *httptest.ResponseRecorder, status int) api.FinancialBudgetResponse {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("budget response: %d %s", rec.Code, rec.Body)
	}
	var out api.FinancialBudgetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" || out.EnforcementReady {
		t.Fatalf("unsafe policy response: %+v", out)
	}
	return out
}

// adr: 530 — revisions protect concurrent edits; deletion retains identity,
// audit and separate holds. A draft never asserts operational protection.
func TestFinancialBudgetCRUD(t *testing.T) {
	e := setup(t, api.PlanHobby)
	spec := financialDraftSpec()
	create := api.CreateFinancialBudgetRequest{Spec: spec}
	headers := map[string]string{"Idempotency-Key": "create-budget"}
	first := e.do(t, "POST", "/v1/billing/budgets", create, headers)
	p := decodeFinancialBudget(t, first, http.StatusCreated)
	if p.Revision != 1 || p.Status != "draft" || p.AccountID != e.acct.ID || p.Spec.Enabled {
		t.Fatalf("draft: %+v", p)
	}
	replay := e.do(t, "POST", "/v1/billing/budgets", create, headers)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay changed: %d %s", replay.Code, replay.Body)
	}
	path := "/v1/billing/budgets/" + p.ID
	read := decodeFinancialBudget(t, e.do(t, "GET", path, nil, nil), http.StatusOK)
	if read.ID != p.ID {
		t.Fatalf("read: %+v", read)
	}
	spec.LimitMillicents *= 2
	update := api.UpdateFinancialBudgetRequest{ExpectedRevision: 1, Spec: spec}
	p = decodeFinancialBudget(t, e.do(t, "PUT", path, update, map[string]string{"Idempotency-Key": "update-budget"}), http.StatusOK)
	if p.Revision != 2 || p.Spec.LimitMillicents != spec.LimitMillicents {
		t.Fatalf("update: %+v", p)
	}
	stale := e.do(t, "PUT", path, update, map[string]string{"Idempotency-Key": "stale-update"})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale edit: %d %s", stale.Code, stale.Body)
	}
	deleted := decodeFinancialBudget(t, e.do(t, "DELETE", path, api.DeleteFinancialBudgetRequest{ExpectedRevision: 2}, map[string]string{"Idempotency-Key": "delete-budget"}), http.StatusOK)
	if deleted.Status != "deleted" || deleted.Revision != 3 || deleted.DeletedAt == nil {
		t.Fatalf("delete: %+v", deleted)
	}
	list := e.do(t, "GET", "/v1/billing/budgets", nil, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"budgets":[]`) {
		t.Fatalf("tombstone listed: %d %s", list.Code, list.Body)
	}
	history := e.do(t, "GET", path+"/revisions?after_revision=0&limit=2", nil, nil)
	var page api.FinancialBudgetHistoryResponse
	if err := json.Unmarshal(history.Body.Bytes(), &page); err != nil || history.Code != http.StatusOK || len(page.Revisions) != 2 || page.NextRevision != 2 {
		t.Fatalf("history: %d %s %v", history.Code, history.Body, err)
	}
	if !strings.HasPrefix(page.Revisions[0].Actor, "api_key:") || page.Revisions[0].Mutation != "created" || page.Revisions[1].Mutation != "updated" {
		t.Fatalf("audit: %+v", page)
	}
	last := e.do(t, "GET", path+"/revisions?after_revision=2&limit=2", nil, nil)
	page = api.FinancialBudgetHistoryResponse{}
	if err := json.Unmarshal(last.Body.Bytes(), &page); err != nil || last.Code != http.StatusOK || len(page.Revisions) != 1 || page.Revisions[0].Mutation != "deleted" || page.NextRevision != 0 {
		t.Fatalf("history continuation: %s %v", last.Body, err)
	}
}

// adr: 530 — creation identity survives a lost HTTP replay cache.
func TestFinancialBudgetCreateRetryWithoutReplayCache(t *testing.T) {
	e := setup(t, api.PlanHobby)
	body, _ := json.Marshal(api.CreateFinancialBudgetRequest{Spec: financialDraftSpec()})
	var first api.FinancialBudgetResponse
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/v1/billing/budgets", bytes.NewReader(body))
		r.Header.Set("Idempotency-Key", "stable-operation")
		r = r.WithContext(authmw.WithPrincipal(r.Context(), e.acct, &state.APIKey{ID: uuid.NewString()}, nil))
		rec := httptest.NewRecorder()
		e.s.createFinancialBudget(rec, r, e.acct)
		p := decodeFinancialBudget(t, rec, http.StatusCreated)
		if i == 0 {
			first = p
		} else if p.ID != first.ID {
			t.Fatalf("duplicate identity: %+v %+v", first, p)
		}
	}
	rows, _ := e.store.ListFinancialBudgets(t.Context(), e.acct.ID)
	history, _ := e.store.ListFinancialBudgetRevisions(t.Context(), e.acct.ID, first.ID, 0, 10)
	if len(rows) != 1 || len(history) != 1 {
		t.Fatalf("retry duplicated audit: %d %d", len(rows), len(history))
	}
}

// adr: 530 — read-only credentials cannot mutate; foreign IDs do not disclose
// policies; unsupported activation leaves no intent or audit behind.
func TestFinancialBudgetAuthorizationAndActivation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	spec := financialDraftSpec()
	enabled := spec
	enabled.Enabled = true
	bad := e.do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: enabled}, map[string]string{"Idempotency-Key": "activate"})
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), "financial_budget_activation_unavailable") {
		t.Fatalf("activation: %d %s", bad.Code, bad.Body)
	}
	rows, _ := e.store.ListFinancialBudgets(t.Context(), e.acct.ID)
	if len(rows) != 0 {
		t.Fatal("activation persisted unavailable protection")
	}
	p := decodeFinancialBudget(t, e.do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "draft"}), http.StatusCreated)
	path := "/v1/billing/budgets/" + p.ID
	key, hash, _ := api.GenerateAPIKey()
	_, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "billing-reader", []string{api.ScopeUsageRead})
	if err != nil {
		t.Fatal(err)
	}
	e.key = key
	for _, read := range []string{"/v1/billing/budgets", path, path + "/revisions"} {
		if rec := e.do(t, "GET", read, nil, nil); rec.Code != http.StatusOK {
			t.Fatalf("read-only: %d %s", rec.Code, rec.Body)
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		writePath := path
		if method == "POST" {
			writePath = "/v1/billing/budgets"
		}
		if rec := e.do(t, method, writePath, nil, map[string]string{"Idempotency-Key": uuid.NewString()}); rec.Code != http.StatusForbidden {
			t.Fatalf("reader mutation: %d %s", rec.Code, rec.Body)
		}
	}
	foreign, _ := e.store.CreateAccount(t.Context(), "foreign-budget@example.com", api.PlanHobby)
	foreignPolicy, err := e.store.CreateFinancialBudget(t.Context(), foreign.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/revisions"} {
		if rec := e.do(t, "GET", "/v1/billing/budgets/"+foreignPolicy.ID+suffix, nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("foreign policy leak: %d %s", rec.Code, rec.Body)
		}
	}
}

// adr: 530 — malformed input cannot bypass revision, bounds or ownership.
func TestFinancialBudgetValidation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	spec := financialDraftSpec()
	for _, key := range []string{"", strings.Repeat("x", api.FinancialBudgetOperationKeyBytes+1)} {
		if rec := e.do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": key}); rec.Code != http.StatusBadRequest {
			t.Fatalf("key: %d %s", rec.Code, rec.Body)
		}
	}
	p := decodeFinancialBudget(t, e.do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "valid"}), http.StatusCreated)
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?after_revision=-1", "?after_revision=9007199254740992", "?after_revision=", "?account_id=other"} {
		if rec := e.do(t, "GET", "/v1/billing/budgets/"+p.ID+"/revisions"+query, nil, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("query %s: %d %s", query, rec.Code, rec.Body)
		}
	}
	for _, revision := range []int64{0, -1, api.FinancialBudgetRevisionMax} {
		if rec := e.do(t, "PUT", "/v1/billing/budgets/"+p.ID, api.UpdateFinancialBudgetRequest{ExpectedRevision: revision, Spec: spec}, map[string]string{"Idempotency-Key": uuid.NewString()}); rec.Code != http.StatusBadRequest {
			t.Fatalf("revision: %d %s", rec.Code, rec.Body)
		}
	}
	if err := e.store.UpdateAccountStatus(t.Context(), e.acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/billing/costs", "/v1/billing/forecast", "/v1/billing/budgets/" + p.ID} {
		if rec := e.do(t, "GET", path, nil, nil); rec.Code != http.StatusOK {
			t.Fatalf("suspended billing read: %d %s", rec.Code, rec.Body)
		}
	}
	rec := e.do(t, "DELETE", "/v1/billing/budgets/"+p.ID, api.DeleteFinancialBudgetRequest{ExpectedRevision: 1}, map[string]string{"Idempotency-Key": "suspended-delete"})
	decodeFinancialBudget(t, rec, http.StatusOK)
	acct, _ := e.store.AccountByID(t.Context(), e.acct.ID)
	if acct.Status != state.AccountSuspended {
		t.Fatal("policy deletion cleared payment hold")
	}
}

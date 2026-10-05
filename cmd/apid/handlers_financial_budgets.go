package main

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Activation is unavailable until ADR-566's component-owner acceptance gates
// pass. Draft management must never promise that a saved policy stops usage.
func financialBudgetResponse(p state.FinancialBudget) api.FinancialBudgetResponse {
	status := "draft"
	if p.Spec.Enabled {
		status = "unavailable"
	}
	if p.DeletedAt != nil {
		status = "deleted"
	}
	return api.FinancialBudgetResponse{ID: p.ID, AccountID: p.AccountID, Revision: p.Revision, Spec: p.Spec, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, DeletedAt: p.DeletedAt, Status: status, EnforcementReady: false, Reasons: []string{"enforcement_integration_pending"}}
}

func (s *server) financialBudgetStore(w http.ResponseWriter, r *http.Request, resource bool) (state.FinancialBudgetStore, bool) {
	if r.URL.RawQuery != "" && !strings.HasSuffix(r.URL.Path, "/revisions") {
		api.WriteProblem(w, api.ErrValidation("budget policy operations accept no query parameters"))
		return nil, false
	}
	if resource && uuid.Validate(r.PathValue("id")) != nil {
		api.WriteProblem(w, api.ErrValidation("budget id must be a UUID"))
		return nil, false
	}
	store, ok := s.store.(state.FinancialBudgetStore)
	if !ok {
		api.WriteProblem(w, financialProblem(errors.New("financial budget store unavailable")))
	}
	return store, ok
}

func (s *server) listFinancialBudgets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetStore(w, r, false)
	if !ok {
		return
	}
	rows, err := store.ListFinancialBudgets(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	out := api.FinancialBudgetListResponse{Budgets: []api.FinancialBudgetResponse{}}
	for _, row := range rows {
		out.Budgets = append(out.Budgets, financialBudgetResponse(row))
	}
	writeFinancialBudgetJSON(w, http.StatusOK, out)
}

func (s *server) getFinancialBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetStore(w, r, true)
	if !ok {
		return
	}
	row, err := store.GetFinancialBudget(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	writeFinancialBudgetJSON(w, http.StatusOK, financialBudgetResponse(row))
}

func (s *server) createFinancialBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetStore(w, r, false)
	if !ok {
		return
	}
	var req api.CreateFinancialBudgetRequest
	if err := decodeJSONSized(r, &req, api.FinancialBudgetSpecBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("expected one bounded budget spec"))
		return
	}
	if !validateFinancialBudgetActivation(w, r, store, acct.ID, req.Spec) {
		return
	}
	// Deriving identity from the operation key also prevents a second policy
	// if the process crashes after the intent transaction but before caching
	// the HTTP response, or if a retry arrives after replay-cache retention.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("financial-budget\n"+acct.ID+"\n"+r.Header.Get("Idempotency-Key"))).String()
	row, err := store.CreateFinancialBudget(r.Context(), acct.ID, id, financialBudgetActor(r), req.Spec)
	if errors.Is(err, state.ErrConflict) {
		old, readErr := store.GetFinancialBudget(r.Context(), acct.ID, id)
		if readErr == nil && old.DeletedAt == nil && old.Revision == 1 && financialBudgetSpecsEqual(old.Spec, req.Spec) {
			writeFinancialBudgetJSON(w, http.StatusCreated, financialBudgetResponse(old))
			return
		}
	}
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	writeFinancialBudgetJSON(w, http.StatusCreated, financialBudgetResponse(row))
}

func (s *server) updateFinancialBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetStore(w, r, true)
	if !ok {
		return
	}
	var req api.UpdateFinancialBudgetRequest
	if err := decodeJSONSized(r, &req, api.FinancialBudgetSpecBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("expected budget spec and expected_revision"))
		return
	}
	if !validFinancialBudgetExpectedRevision(req.ExpectedRevision) {
		api.WriteProblem(w, api.ErrValidation("expected_revision must be the policy's current revision"))
		return
	}
	if !validateFinancialBudgetActivation(w, r, store, acct.ID, req.Spec) {
		return
	}
	row, err := store.UpdateFinancialBudget(r.Context(), acct.ID, r.PathValue("id"), req.ExpectedRevision, financialBudgetActor(r), req.Spec)
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	writeFinancialBudgetJSON(w, http.StatusOK, financialBudgetResponse(row))
}

func (s *server) deleteFinancialBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetStore(w, r, true)
	if !ok {
		return
	}
	var req api.DeleteFinancialBudgetRequest
	if err := decodeJSONSized(r, &req, api.FinancialBudgetSpecBytes); err != nil || !validFinancialBudgetExpectedRevision(req.ExpectedRevision) {
		api.WriteProblem(w, api.ErrValidation("expected_revision must be the policy's current revision"))
		return
	}
	row, err := store.DeleteFinancialBudget(r.Context(), acct.ID, r.PathValue("id"), req.ExpectedRevision, financialBudgetActor(r))
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	writeFinancialBudgetJSON(w, http.StatusOK, financialBudgetResponse(row))
}

func (s *server) listFinancialBudgetRevisions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.financialBudgetStore(w, r, true)
	if !ok {
		return
	}
	after, limit, err := financialBudgetHistoryQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("expected after_revision >= 0 and limit between 1 and 100"))
		return
	}
	if _, err := store.GetFinancialBudget(r.Context(), acct.ID, r.PathValue("id")); err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	rows, err := store.ListFinancialBudgetRevisions(r.Context(), acct.ID, r.PathValue("id"), after, limit)
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	out := api.FinancialBudgetHistoryResponse{Revisions: []api.FinancialBudgetRevisionResponse{}}
	for _, row := range rows {
		out.Revisions = append(out.Revisions, api.FinancialBudgetRevisionResponse{PolicyID: row.PolicyID, Revision: row.Revision, Actor: row.Actor, Mutation: row.Mutation, Spec: row.Spec, RecordedAt: row.RecordedAt})
	}
	if len(rows) == limit {
		out.NextRevision = rows[len(rows)-1].Revision
	}
	writeFinancialBudgetJSON(w, http.StatusOK, out)
}

func validateFinancialBudgetActivation(w http.ResponseWriter, r *http.Request, store state.FinancialBudgetStore, account string, spec financial.BudgetSpec) bool {
	if err := store.ValidateFinancialBudgetScope(r.Context(), account, spec); err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return false
	}
	if spec.Enabled {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "financial_budget_activation_unavailable", "Budget activation unavailable", "Save enabled=false to retain a draft. Activation requires accepted gateway, scheduler and metering integrations; this deployment cannot enforce the selected response."))
		return false
	}
	return true
}

func financialBudgetActor(r *http.Request) string {
	acct, key, membership, _ := authmw.PrincipalFrom(r)
	if key != nil {
		return "api_key:" + key.ID
	}
	if membership != nil {
		return "account:" + membership.AccountID
	}
	return "account:" + acct.ID
}

func financialBudgetSpecsEqual(a, b financial.BudgetSpec) bool {
	return a.Name == b.Name && a.Scope == b.Scope && a.Currency == b.Currency && slices.Equal(a.Meters, b.Meters) && a.Basis == b.Basis && a.LimitMillicents == b.LimitMillicents && slices.Equal(a.NotifyMillicents, b.NotifyMillicents) && a.Mode == b.Mode && a.Action == b.Action && a.DrainSeconds == b.DrainSeconds && a.ResumeRule == b.ResumeRule && a.Enabled == b.Enabled
}

func validFinancialBudgetExpectedRevision(revision int64) bool {
	return revision >= 1 && revision < api.FinancialBudgetRevisionMax
}

func financialBudgetHistoryQuery(raw string) (int64, int, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, err
	}
	for key, values := range q {
		if (key != "after_revision" && key != "limit") || len(values) != 1 {
			return 0, 0, state.ErrInvalidArgument
		}
	}
	after, limit := int64(0), api.FinancialBudgetHistoryMax
	if values, ok := q["after_revision"]; ok {
		after, err = strconv.ParseInt(values[0], 10, 64)
	}
	if err != nil || after < 0 || after > api.FinancialBudgetRevisionMax {
		return 0, 0, state.ErrInvalidArgument
	}
	if values, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(values[0])
	}
	if err != nil || limit < 1 || limit > api.FinancialBudgetHistoryMax {
		return 0, 0, state.ErrInvalidArgument
	}
	return after, limit, nil
}

func financialBudgetProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrConflict) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Budget revision conflict", "Read the current policy before retrying. A stale revision or reused operation identity cannot overwrite it.")
	}
	if errors.Is(err, state.ErrFinancialBudgetLimit) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Budget policy limit exceeded", "Delete an unused policy before creating another.").WithLimit(api.FinancialBudgetsPerAccount, api.FinancialBudgetsPerAccount+1)
	}
	if errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrNotFound) {
		return financialProblem(err)
	}
	return api.ErrCapacity("could not commit or read budget policy state")
}

func writeFinancialBudgetJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, status, body)
}

func (s *server) requireFinancialBudgetIdempotency(next accountHandler) accountHandler {
	idempotent := s.idempotent(next)
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		key := r.Header.Get("Idempotency-Key")
		if strings.TrimSpace(key) == "" || len(key) > api.FinancialBudgetOperationKeyBytes {
			api.WriteProblem(w, api.ErrValidation("budget mutations require an Idempotency-Key of 1..255 characters"))
			return
		}
		idempotent(w, r, acct)
	}
}

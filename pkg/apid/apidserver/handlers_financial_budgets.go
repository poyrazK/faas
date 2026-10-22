package apidserver

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/financialbudgets"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) writeFinancialBudget(w http.ResponseWriter, r *http.Request, status int, p state.FinancialBudget) {
	out, err := financialbudgets.Response(r.Context(), s.store, p, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	writeFinancialBudgetJSON(w, status, out)
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
		response, err := financialbudgets.Response(r.Context(), s.store, row, time.Now().UTC())
		if err != nil {
			api.WriteProblem(w, financialBudgetProblem(err))
			return
		}
		out.Budgets = append(out.Budgets, response)
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
	s.writeFinancialBudget(w, r, http.StatusOK, row)
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
	req.Spec = state.NormalizeFinancialBudgetSpec(req.Spec)
	if problem := s.strictBudgetReleaseProblem(acct, req.Spec, nil); problem != nil {
		api.WriteProblem(w, problem)
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
			s.writeFinancialBudget(w, r, http.StatusCreated, old)
			return
		}
	}
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	s.writeFinancialBudget(w, r, http.StatusCreated, row)
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
	req.Spec = state.NormalizeFinancialBudgetSpec(req.Spec)
	if strictBudgetActivation(req.Spec) && !s.platformFlagEnabled(platformflags.StrictBudgets, acct) {
		current, err := store.GetFinancialBudget(r.Context(), acct.ID, r.PathValue("id"))
		if err != nil {
			api.WriteProblem(w, financialBudgetProblem(err))
			return
		}
		// The exemption holds only for the revision this update replaces.
		if current.Revision != req.ExpectedRevision {
			current = state.FinancialBudget{}
		}
		if problem := s.strictBudgetReleaseProblem(acct, req.Spec, &current); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	if !validateFinancialBudgetActivation(w, r, store, acct.ID, req.Spec) {
		return
	}
	row, err := store.UpdateFinancialBudget(r.Context(), acct.ID, r.PathValue("id"), req.ExpectedRevision, financialBudgetActor(r), req.Spec)
	if err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return
	}
	s.writeFinancialBudget(w, r, http.StatusOK, row)
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
	s.writeFinancialBudget(w, r, http.StatusOK, row)
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

func strictBudgetActivation(spec financial.BudgetSpec) bool {
	return spec.Enabled && spec.Mode == "strict"
}

// strictBudgetReleaseProblem refuses enabling a strict budget the
// strict_budgets platform flag (ADR-1750) has not released to the account.
// A strict budget that is already enabled stays editable, and disabling or
// deleting one is never refused.
func (s *server) strictBudgetReleaseProblem(acct state.Account, spec financial.BudgetSpec, current *state.FinancialBudget) *api.Problem {
	if !strictBudgetActivation(spec) || s.platformFlagEnabled(platformflags.StrictBudgets, acct) {
		return nil
	}
	if current != nil && current.DeletedAt == nil && strictBudgetActivation(current.Spec) {
		return nil
	}
	return api.NewProblem(http.StatusUnprocessableEntity, "financial_budget_activation_unavailable", "Strict budgets not released",
		"Strict compute and egress budgets are not yet available to this account. Monitored budgets and alerts are unaffected, and strict budgets that are already enabled keep enforcing.")
}

func validateFinancialBudgetActivation(w http.ResponseWriter, r *http.Request, store state.FinancialBudgetStore, account string, spec financial.BudgetSpec) bool {
	if err := store.ValidateFinancialBudgetScope(r.Context(), account, spec); err != nil {
		api.WriteProblem(w, financialBudgetProblem(err))
		return false
	}
	if spec.Enabled {
		if financial.StrictCompute(spec) {
			if _, ok := store.(state.FinancialComputeStore); ok {
				return true
			}
		}
		_, monitoring := store.(state.FinancialBudgetMonitorStore)
		_, stopping := store.(state.FinancialBudgetStopStore)
		_, commandOwnership := store.(state.FinancialPreviewCommandStore)
		if monitoring && (financial.NotificationOnly(spec) || stopping && commandOwnership && financial.PreviewStopping(spec)) {
			return true
		}
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "financial_budget_activation_unavailable", "Budget activation unavailable", "Activate monitored notify, monitored compute-only stop_previews, or strict suspend_workloads (app/job gross compute or account net compute optionally combined with egress) with zero drain and no alert thresholds. Other policies must remain disabled drafts."))
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
	a = state.NormalizeFinancialBudgetSpec(a)
	b = state.NormalizeFinancialBudgetSpec(b)
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
	if errors.Is(err, financial.ErrComputeUnavailable) {
		return api.NewProblem(422, "financial_compute_not_ready", "Strict compute activation unavailable", "Activation requires quiet covered workloads, complete retained accounting, and current capable native owners on every active node. Account budgets require recorded allowances and funded-job owners. Combining compute and egress also requires complete egress accounting and byte-enforcement owners.")
	}
	if errors.Is(err, financial.ErrComputeBudget) {
		return api.NewProblem(422, "financial_compute_budget_too_low", "Compute budget below committed funding", "The limit must cover this month's committed and reserved funding for every selected meter. Changing a policy does not reset spending.")
	}
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

package operation

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func OperationWorkflowPerformanceInstanceOptions(r *http.Request, operator bool, appID string) (api.OperationWorkflowPerformanceInstanceOptions, error) {
	var opts api.OperationWorkflowPerformanceInstanceOptions
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opts, state.ErrInvalidArgument
	}
	base := url.Values{}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return opts, state.ErrInvalidArgument
		}
		switch key {
		case "scope", "workflow", "tenant_id", "app_id", "baseline_token":
			base[key] = values
		case "gap_status", "condition_evidence_status", "condition_id", "condition_description", "condition_result", "cohort", "dimension", "state", "operation", "code", "owner", "cohort_token", "kind", "decision_code", "rule_id", "rule_version", "group_id", "path_id", "outcome_code":
		case "contract_version":
			opts.ContractVersion, err = strconv.Atoi(values[0])
			if err != nil || opts.ContractVersion < 1 {
				return opts, state.ErrInvalidArgument
			}
		case "unassigned":
			if values[0] != "true" {
				return opts, state.ErrInvalidArgument
			}
			opts.Unassigned = true
		default:
			return opts, state.ErrInvalidArgument
		}
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = base.Encode()
	opts.OperationWorkflowPerformanceOptions, err = OperationWorkflowPerformanceOptions(clone, operator, appID)
	if err != nil {
		return opts, err
	}
	opts.Cohort, opts.Dimension, opts.State, opts.Operation, opts.Code, opts.Owner, opts.CohortToken = q.Get("cohort"), q.Get("dimension"), q.Get("state"), q.Get("operation"), q.Get("code"), q.Get("owner"), q.Get("cohort_token")
	opts.GapStatus = q.Get("gap_status")
	opts.ConditionEvidenceStatus = q.Get("condition_evidence_status")
	opts.ConditionID, opts.ConditionDescription, opts.ConditionResult = q.Get("condition_id"), q.Get("condition_description"), q.Get("condition_result")
	opts.Kind, opts.DecisionCode, opts.RuleID, opts.RuleVersion, opts.GroupID, opts.PathID, opts.OutcomeCode = q.Get("kind"), q.Get("decision_code"), q.Get("rule_id"), q.Get("rule_version"), q.Get("group_id"), q.Get("path_id"), q.Get("outcome_code")
	return opts, nil
}
func (s *Service) ListAccountWorkflowPerformanceInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowPerformanceInstanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := OperationWorkflowPerformanceInstanceOptions(r, true, app.ID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	out, err := store.ListAccountWorkflowPerformanceInstances(r.Context(), acct.ID, opts)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (s *Service) ListPlatformTenantSelfWorkflowPerformanceInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowPerformanceInstanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := OperationWorkflowPerformanceInstanceOptions(r, false, "")
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	out, err := store.ListPlatformTenantWorkflowPerformanceInstances(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

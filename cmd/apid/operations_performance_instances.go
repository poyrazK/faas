package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func operationWorkflowPerformanceInstanceOptions(r *http.Request, operator bool, appID string) (api.OperationWorkflowPerformanceInstanceOptions, error) {
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
		case "scope", "workflow", "tenant_id", "app_id":
			base[key] = values
		case "cohort", "dimension", "state", "operation", "code", "owner", "cohort_token":
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
	opts.OperationWorkflowPerformanceOptions, err = operationWorkflowPerformanceOptions(clone, operator, appID)
	if err != nil {
		return opts, err
	}
	opts.Cohort, opts.Dimension, opts.State, opts.Operation, opts.Code, opts.Owner, opts.CohortToken = q.Get("cohort"), q.Get("dimension"), q.Get("state"), q.Get("operation"), q.Get("code"), q.Get("owner"), q.Get("cohort_token")
	return opts, nil
}
func (s *server) listAccountWorkflowPerformanceInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowPerformanceInstanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := operationWorkflowPerformanceInstanceOptions(r, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.ListAccountWorkflowPerformanceInstances(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) listPlatformTenantSelfWorkflowPerformanceInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowPerformanceInstanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := operationWorkflowPerformanceInstanceOptions(r, false, "")
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.ListPlatformTenantWorkflowPerformanceInstances(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

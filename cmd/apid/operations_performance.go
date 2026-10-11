package main

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func operationWorkflowPerformanceOptions(r *http.Request, operator bool, appID string) (api.OperationWorkflowPerformanceOptions, error) {
	opts := api.OperationWorkflowPerformanceOptions{AppID: appID}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opts, state.ErrInvalidArgument
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return opts, state.ErrInvalidArgument
		}
		switch key {
		case "scope", "workflow":
		case "tenant_id":
			if !operator {
				return opts, state.ErrInvalidArgument
			}
			opts.TenantID = values[0]
		case "app_id":
			if operator {
				return opts, state.ErrInvalidArgument
			}
			opts.AppID = values[0]
		default:
			return opts, state.ErrInvalidArgument
		}
	}
	opts.Scope, opts.Workflow = q.Get("scope"), q.Get("workflow")
	return opts, nil
}
func (s *server) summarizeAccountWorkflowPerformance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowPerformanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := operationWorkflowPerformanceOptions(r, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.SummarizeAccountWorkflowPerformance(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) summarizePlatformTenantSelfWorkflowPerformance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowPerformanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := operationWorkflowPerformanceOptions(r, false, "")
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.SummarizePlatformTenantWorkflowPerformance(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

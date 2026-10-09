package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
	"strconv"
)

func operationWorkflowOutcomeOptions(r *http.Request, operator bool, appID string, summary bool) (api.OperationWorkflowOutcomeSummaryOptions, error) {
	opts := api.OperationWorkflowOutcomeSummaryOptions{OperationWorkflowOutcomeOptions: api.OperationWorkflowOutcomeOptions{AppID: appID}}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opts, state.ErrInvalidArgument
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return opts, state.ErrInvalidArgument
		}
		switch key {
		case "scope", "workflow", "code", "limit", "cursor":
		case "group_by":
			if !summary {
				return opts, state.ErrInvalidArgument
			}
		case "app_id":
			if operator {
				return opts, state.ErrInvalidArgument
			}
			opts.AppID = values[0]
		case "tenant_id":
			if !operator {
				return opts, state.ErrInvalidArgument
			}
			opts.TenantID = values[0]
		default:
			return opts, state.ErrInvalidArgument
		}
	}
	opts.Scope, opts.Workflow, opts.Code, opts.Cursor, opts.GroupBy = query.Get("scope"), query.Get("workflow"), query.Get("code"), query.Get("cursor"), query.Get("group_by")
	if query.Has("limit") {
		opts.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || opts.Limit < 1 {
			return opts, state.ErrInvalidArgument
		}
	}
	return opts, nil
}

func (s *server) listAccountWorkflowOutcomes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowOutcomeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow outcomes unavailable"))
		return
	}
	opts, err := operationWorkflowOutcomeOptions(r, true, app.ID, false)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.ListAccountWorkflowOutcomes(r.Context(), acct.ID, opts.OperationWorkflowOutcomeOptions)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) summarizeAccountWorkflowOutcomes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowOutcomeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow outcomes unavailable"))
		return
	}
	opts, err := operationWorkflowOutcomeOptions(r, true, app.ID, true)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.SummarizeAccountWorkflowOutcomes(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) listPlatformTenantSelfWorkflowOutcomes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowOutcomeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow outcomes unavailable"))
		return
	}
	opts, err := operationWorkflowOutcomeOptions(r, false, "", false)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.ListPlatformTenantWorkflowOutcomes(r.Context(), acct.ID, tenant, opts.OperationWorkflowOutcomeOptions)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) summarizePlatformTenantSelfWorkflowOutcomes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowOutcomeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow outcomes unavailable"))
		return
	}
	opts, err := operationWorkflowOutcomeOptions(r, false, "", true)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	out, err := store.SummarizePlatformTenantWorkflowOutcomes(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

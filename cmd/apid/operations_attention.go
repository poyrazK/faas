package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
	"strconv"
)

func operationWorkflowAttentionOptions(r *http.Request, operator bool, appID string) (api.OperationWorkflowAttentionOptions, error) {
	opts := api.OperationWorkflowAttentionOptions{AppID: appID}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opts, state.ErrInvalidArgument
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return opts, state.ErrInvalidArgument
		}
		switch key {
		case "scope", "workflow", "target_operation", "reason", "limit", "cursor", "blocker_code", "dependency_status", "required_outcome_code":
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
	opts.Scope, opts.Workflow, opts.TargetOperation, opts.Reason, opts.Cursor = query.Get("scope"), query.Get("workflow"), query.Get("target_operation"), query.Get("reason"), query.Get("cursor")
	opts.BlockerCode = query.Get("blocker_code")
	opts.DependencyStatus, opts.RequiredOutcomeCode = query.Get("dependency_status"), query.Get("required_outcome_code")
	if query.Has("limit") {
		opts.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || opts.Limit < 1 {
			return opts, state.ErrInvalidArgument
		}
	}
	return opts, nil
}

func (s *server) listAccountWorkflowAttention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowAttentionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow attention unavailable"))
		return
	}
	opts, err := operationWorkflowAttentionOptions(r, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page, err := store.ListAccountWorkflowAttention(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *server) listPlatformTenantSelfWorkflowAttention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowAttentionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow attention unavailable"))
		return
	}
	opts, err := operationWorkflowAttentionOptions(r, false, "")
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page, err := store.ListPlatformTenantWorkflowAttention(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func operationWorkflowAttentionSummaryOptions(r *http.Request, operator bool, appID string) (api.OperationWorkflowAttentionSummaryOptions, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return api.OperationWorkflowAttentionSummaryOptions{}, state.ErrInvalidArgument
	}
	by := query.Get("group_by")
	if values, ok := query["group_by"]; ok && (len(values) != 1 || by == "") {
		return api.OperationWorkflowAttentionSummaryOptions{}, state.ErrInvalidArgument
	}
	query.Del("group_by")
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = query.Encode()
	opts, err := operationWorkflowAttentionOptions(clone, operator, appID)
	return api.OperationWorkflowAttentionSummaryOptions{OperationWorkflowAttentionOptions: opts, GroupBy: by}, err
}

func (s *server) summarizeAccountWorkflowAttention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowAttentionSummaryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow summaries unavailable"))
		return
	}
	opts, err := operationWorkflowAttentionSummaryOptions(r, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	result, err := store.SummarizeAccountWorkflowAttention(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) summarizePlatformTenantSelfWorkflowAttention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowAttentionSummaryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow summaries unavailable"))
		return
	}
	opts, err := operationWorkflowAttentionSummaryOptions(r, false, "")
	if err != nil {
		writeOperationError(w, err)
		return
	}
	result, err := store.SummarizePlatformTenantWorkflowAttention(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

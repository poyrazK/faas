package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
)

func operationWorkflowReadinessQueueOptions(r *http.Request, operator bool, app string) (api.OperationWorkflowReadinessQueueOptions, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return api.OperationWorkflowReadinessQueueOptions{}, state.ErrInvalidArgument
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return api.OperationWorkflowReadinessQueueOptions{}, state.ErrInvalidArgument
		}
		switch key {
		case "app_id", "scope", "tenant_id", "workflow", "target_operation", "owner", "unassigned", "requirement", "limit":
		default:
			return api.OperationWorkflowReadinessQueueOptions{}, state.ErrInvalidArgument
		}
	}
	requirement := q.Get("requirement")
	q.Del("requirement")
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = q.Encode()
	base, err := operationWorkflowAttentionOptions(clone, operator, app)
	return api.OperationWorkflowReadinessQueueOptions{AppID: base.AppID, Scope: base.Scope, TenantID: base.TenantID, Workflow: base.Workflow, TargetOperation: base.TargetOperation, Owner: base.Owner, Unassigned: base.Unassigned, Limit: base.Limit, Requirement: requirement}, err
}
func (s *server) listAccountWorkflowReadinessQueue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowReadinessQueueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness queue unavailable"))
		return
	}
	opts, err := operationWorkflowReadinessQueueOptions(r, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page, err := store.ListAccountWorkflowReadinessQueue(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *server) listPlatformTenantSelfWorkflowReadinessQueue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowReadinessQueueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness queue unavailable"))
		return
	}
	opts, err := operationWorkflowReadinessQueueOptions(r, false, "")
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page, err := store.ListPlatformTenantWorkflowReadinessQueue(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

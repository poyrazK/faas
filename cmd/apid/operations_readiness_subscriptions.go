package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountWorkflowReadinessSubscriptions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.workflowReadinessSubscriptions(w, r, acct, "", app.ID, true)
}
func (s *server) customerWorkflowReadinessSubscriptions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	s.workflowReadinessSubscriptions(w, r, acct, tenant, "", false)
}
func (s *server) workflowReadinessSubscriptions(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowReadinessSubscriptionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness subscriptions unavailable"))
		return
	}
	if r.PathValue("sequence") != "" {
		s.workflowReadinessNotification(w, r, acct, tenant, app, operator, store)
		return
	}
	if r.Method == http.MethodPost {
		s.createWorkflowReadinessSubscription(w, r, acct, tenant, app, operator, store)
		return
	}
	scope, selectedTenant, selectedApp, after, limit, err := readinessSubscriptionQuery(r, operator, app)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if operator {
		tenant = selectedTenant
	}
	app = selectedApp
	s.readWorkflowReadinessSubscription(w, r, acct, tenant, app, scope, after, limit, operator, store)
}
func (s *server) readWorkflowReadinessSubscription(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app, scope string, after int64, limit int, operator bool, store state.OperationWorkflowReadinessSubscriptionStore) {
	var err error
	if r.Method == http.MethodDelete {
		if err = store.DeleteWorkflowReadinessSubscription(r.Context(), acct.ID, tenant, app, scope, r.PathValue("id"), operator); err != nil {
			writeOperationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.PathValue("id") != "" {
		out, err := store.WorkflowReadinessInbox(r.Context(), acct.ID, tenant, app, scope, r.PathValue("id"), after, limit, operator)
		if err != nil {
			writeOperationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out, err := store.ListWorkflowReadinessSubscriptions(r.Context(), acct.ID, tenant, app, scope, operator)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) createWorkflowReadinessSubscription(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool, store state.OperationWorkflowReadinessSubscriptionStore) {
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	var req api.OperationWorkflowReadinessSubscriptionRequest
	if !decodeOperationBody(w, r, &req, api.OperationWorkflowReadinessSubscriptionBodyMaxBytes) {
		return
	}
	if !operator {
		app = req.AppID
	}
	out, err := store.CreateWorkflowReadinessSubscription(r.Context(), acct.ID, tenant, app, req, operator)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func readinessSubscriptionQuery(r *http.Request, operator bool, app string) (scope, tenant, selectedApp string, after int64, limit int, err error) {
	err = state.ErrInvalidArgument
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return
	}
	inbox := r.Method == http.MethodGet && r.PathValue("id") != "" && r.PathValue("sequence") == ""
	for k, v := range q {
		if len(v) != 1 || v[0] == "" {
			return
		}
		if k != "scope" && !(operator && k == "tenant_id") && !(!operator && k == "app_id") && !(inbox && (k == "after_sequence" || k == "limit")) {
			return
		}
	}
	scope, tenant, selectedApp = q.Get("scope"), q.Get("tenant_id"), app
	if !operator {
		selectedApp = q.Get("app_id")
	}
	if q.Has("after_sequence") {
		after, e = strconv.ParseInt(q.Get("after_sequence"), 10, 64)
		if e != nil {
			return
		}
	}
	if q.Has("limit") {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil {
			return
		}
	}
	err = nil
	return
}

func (s *server) workflowReadinessNotification(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool, store state.OperationWorkflowReadinessSubscriptionStore) {
	scope, selectedTenant, selectedApp, _, _, err := readinessSubscriptionQuery(r, operator, app)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if operator {
		tenant = selectedTenant
	}
	app = selectedApp
	sequence, err := strconv.ParseInt(r.PathValue("sequence"), 10, 64)
	if err != nil {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	if r.Method == http.MethodGet {
		out, err := store.CheckWorkflowReadinessNotification(r.Context(), acct.ID, tenant, app, scope, r.PathValue("id"), sequence, operator)
		if err != nil {
			writeOperationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	p, ok := principalFrom(r)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("authenticated principal missing"))
		return
	}
	actor := "account:" + p.Acct.ID
	if p.Key != nil {
		actor = "api_key:" + p.Key.ID
	}
	var req api.OperationWorkflowReadinessCoordinationRequest
	if !decodeOperationBody(w, r, &req, api.OperationWorkflowReadinessSubscriptionBodyMaxBytes) {
		return
	}
	out, err := store.UpdateWorkflowReadinessNotification(r.Context(), acct.ID, tenant, app, scope, r.PathValue("id"), sequence, actor, req, operator)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

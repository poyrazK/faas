package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountWorkflowPlaybookProgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.workflowPlaybookProgress(w, r, acct, "", app.ID, true)
}
func (s *server) customerWorkflowPlaybookProgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	s.workflowPlaybookProgress(w, r, acct, tenant, "", false)
}
func (s *server) workflowPlaybookProgress(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowPlaybookProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("playbook progress unavailable"))
		return
	}
	opts, err := workflowCoordinationTimelineOptions(r, app, operator)
	if err != nil || r.URL.Query().Has("offset") || r.URL.Query().Has("limit") {
		operation.WriteOperationError(w, state.ErrInvalidArgument)
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
	var out api.OperationWorkflowPlaybookProgress
	if r.Method == http.MethodPost {
		var req api.OperationWorkflowPlaybookProgressStartRequest
		if !operation.DecodeOperationBody(w, r, &req, api.OperationWorkflowPlaybookProgressBodyMaxBytes) {
			return
		}
		out, err = store.StartWorkflowPlaybookProgress(r.Context(), acct.ID, tenant, opts, actor, req, operator)
	} else {
		req := api.OperationWorkflowPlaybookProgressUpdateRequest{ItemIndex: -1}
		if !operation.DecodeOperationBody(w, r, &req, api.OperationWorkflowPlaybookProgressBodyMaxBytes) {
			return
		}
		out, err = store.UpdateWorkflowPlaybookProgress(r.Context(), acct.ID, tenant, opts, r.PathValue("id"), actor, req, operator)
	}
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

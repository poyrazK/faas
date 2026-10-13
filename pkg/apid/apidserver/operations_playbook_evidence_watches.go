package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountWorkflowPlaybookEvidenceWatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.workflowPlaybookEvidenceWatch(w, r, acct, "", app.ID, true)
}
func (s *server) customerWorkflowPlaybookEvidenceWatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	s.workflowPlaybookEvidenceWatch(w, r, acct, tenant, "", false)
}
func (s *server) workflowPlaybookEvidenceWatch(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowPlaybookEvidenceWatchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("playbook monitoring unavailable"))
		return
	}
	opts, err := workflowCoordinationTimelineOptions(r, app, operator)
	if err != nil || r.URL.Query().Has("offset") || r.URL.Query().Has("limit") {
		operation.WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	principal, ok := principalFrom(r)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("authenticated principal missing"))
		return
	}
	actor := "account:" + principal.Acct.ID
	if principal.Key != nil {
		actor = "api_key:" + principal.Key.ID
	}
	var req api.OperationWorkflowPlaybookEvidenceWatchRequest
	if !operation.DecodeOperationBody(w, r, &req, api.OperationWorkflowPlaybookProgressBodyMaxBytes) {
		return
	}
	out, err := store.ConfigureWorkflowPlaybookEvidenceWatch(r.Context(), acct.ID, tenant, opts, r.PathValue("id"), actor, req, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

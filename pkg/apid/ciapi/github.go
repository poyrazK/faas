package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) githubAvailable(w http.ResponseWriter, acct state.Account) (ci.GitHubStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.GitHubStore)
	if !ok || !h.Config.GitHubAllows(acct.ID) {
		httpkit.NotFound(w, "CI GitHub is not available")
		return nil, false
	}
	return store, true
}

func (h Handler) PutGitHubBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.githubAvailable(w, acct)
	if !ok {
		return
	}
	if !ci.ValidName(r.PathValue("name")) {
		api.WriteProblem(w, api.ErrValidation("invalid CI GitHub binding name"))
		return
	}
	var req ci.GitHubBindingRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return
	}
	req, err := ci.NormalizeGitHubBinding(req)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	revision, err := h.Store.Revision(r.Context(), acct.ID, req.RevisionID)
	if err != nil {
		writeError(w, err)
		return
	}
	if h.VerifyGitHub == nil {
		writeError(w, ci.ErrGitHubUnavailable)
		return
	}
	if err := h.VerifyGitHub(r.Context(), acct.ID, req.InstallationID, req.RepositoryID, revision.Definition.Repository); err != nil {
		writeError(w, err)
		return
	}
	row, err := store.PutGitHubBinding(r.Context(), acct.ID, r.PathValue("name"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.github.binding.saved", map[string]any{"binding_id": row.ID, "revision_id": row.RevisionID, "repository_id": row.RepositoryID})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) ListGitHubBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.githubAvailable(w, acct)
	if !ok {
		return
	}
	rows, err := store.GitHubBindings(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"bindings": rows})
}

func (h Handler) DisableGitHubBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.githubAvailable(w, acct)
	if !ok {
		return
	}
	if !ci.ValidName(r.PathValue("name")) {
		api.WriteProblem(w, api.ErrValidation("invalid CI GitHub binding name"))
		return
	}
	if err := store.DisableGitHubBinding(r.Context(), acct.ID, r.PathValue("name")); err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.github.binding.disabled", map[string]any{"binding_name": r.PathValue("name")})
	w.WriteHeader(http.StatusNoContent)
}

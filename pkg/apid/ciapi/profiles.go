package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) profileStore(w http.ResponseWriter, acct state.Account) (ci.ProfileStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.ProfileStore)
	if !ok {
		httpkit.NotFound(w, "CI profiles are not available")
	}
	return store, ok
}

func (h Handler) PutExecutionProfile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.profileStore(w, acct)
	if !ok {
		return
	}
	if !h.Config.GitHubAllows(acct.ID) {
		httpkit.NotFound(w, "CI profile setup is not available")
		return
	}
	var req ci.ExecutionProfileRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return
	}
	if !ci.ValidName(r.PathValue("name")) || !ci.ValidExecutionProfileRequest(req) {
		api.WriteProblem(w, api.ErrValidation("profile requires a name, revision and binding UUIDs, and job-to-template UUIDs"))
		return
	}
	row, err := ci.SaveExecutionProfile(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, r.PathValue("name"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.profile.saved", map[string]any{"profile_id": row.ID, "revision_id": row.RevisionID, "binding_id": row.BindingID})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) ListExecutionProfiles(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.profileStore(w, acct)
	if !ok {
		return
	}
	rows, err := store.ExecutionProfiles(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"profiles": rows})
}

func (h Handler) ExecutionProfile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.profileStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := store.ExecutionProfile(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) DisableExecutionProfile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.profileStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := store.DisableExecutionProfile(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.profile.disabled", map[string]any{"profile_id": row.ID})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

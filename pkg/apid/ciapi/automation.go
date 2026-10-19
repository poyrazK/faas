package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) automationStore(w http.ResponseWriter, acct state.Account) (ci.AutomationStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.AutomationStore)
	if !ok {
		httpkit.NotFound(w, "CI automation is not available")
	}
	return store, ok
}

func (h Handler) PutAutomationPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.automationStore(w, acct)
	if !ok {
		return
	}
	if !h.Config.GitHubAllows(acct.ID) {
		httpkit.NotFound(w, "CI automation setup is not available")
		return
	}
	var req ci.AutomationRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return
	}
	req, err := ci.NormalizeAutomationRequest(req)
	if err != nil || !ci.ValidID(r.PathValue("binding")) {
		api.WriteProblem(w, api.ErrValidation("automation requires binding/profile UUIDs, exact push or PR base branches, and valid optional path filters"))
		return
	}
	row, err := ci.SaveAutomationPolicy(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, r.PathValue("binding"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.automation.saved", map[string]any{"policy_id": row.ID, "profile_id": row.ProfileID, "generation": row.Generation})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) ListAutomationPolicies(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.automationStore(w, acct)
	if !ok {
		return
	}
	rows, err := store.AutomationPolicies(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"policies": rows})
}

func (h Handler) DisableAutomationPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.automationStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) {
		return
	}
	row, err := store.DisableAutomationPolicy(r.Context(), acct.ID, r.PathValue("binding"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.automation.disabled", map[string]any{"policy_id": row.ID, "binding_id": row.BindingID})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) ListAutomationEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.automationStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) {
		return
	}
	rows, err := store.AutomationEvents(r.Context(), acct.ID, r.PathValue("binding"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"events": rows})
}

package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) automationRecoveryStore(w http.ResponseWriter, acct state.Account) (ci.AutomationRecoveryStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.AutomationRecoveryStore)
	if !ok {
		httpkit.NotFound(w, "CI automation recovery is not available")
	}
	return store, ok
}

func (h Handler) AutomationEventStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.automationRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := store.AutomationEventStatus(r.Context(), acct.ID, r.PathValue("binding"), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func automationRetryRequest(w http.ResponseWriter, r *http.Request) (ci.AutomationRetryRequest, bool) {
	var body struct {
		ExpectedRetryGeneration *int32 `json:"expected_retry_generation"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &body, api.CIRequestMaxBytes) {
		return ci.AutomationRetryRequest{}, false
	}
	if body.ExpectedRetryGeneration == nil || *body.ExpectedRetryGeneration < 0 || *body.ExpectedRetryGeneration > api.CIAutomationRetryMaxGenerations || !ci.ValidIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		api.WriteProblem(w, api.ErrValidation("automation retry requires expected_retry_generation and a bounded printable Idempotency-Key"))
		return ci.AutomationRetryRequest{}, false
	}
	return ci.AutomationRetryRequest{ExpectedRetryGeneration: *body.ExpectedRetryGeneration}, true
}

func (h Handler) RetryAutomationEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.automationRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) || !validID(w, r.PathValue("id")) {
		return
	}
	if !h.Config.GitHubAllows(acct.ID) || !h.Config.AutomationEnabled {
		httpkit.NotFound(w, "CI automation retry is not available")
		return
	}
	req, ok := automationRetryRequest(w, r)
	if !ok {
		return
	}
	row, replay, err := ci.RequestAutomationRetry(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, r.PathValue("binding"), r.PathValue("id"), r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.automation.retry_requested", map[string]any{"retry_id": row.ID, "event_id": row.EventID, "binding_id": r.PathValue("binding"), "generation": row.Generation})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, row)
}

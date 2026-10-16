package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) checkRecoveryStore(w http.ResponseWriter, acct state.Account) (ci.CheckRecoveryStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.CheckRecoveryStore)
	if !ok {
		httpkit.NotFound(w, "CI Check delivery is not available")
	}
	return store, ok
}

func (h Handler) ListCheckDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.checkRecoveryStore(w, acct)
	if !ok {
		return
	}
	rows, err := store.CheckDeliveries(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": rows})
}

func (h Handler) CheckDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.checkRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := store.CheckDelivery(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func checkRetryRequest(w http.ResponseWriter, r *http.Request) (ci.CheckRetryRequest, bool) {
	var body struct {
		ExpectedRetryGeneration *int32 `json:"expected_retry_generation"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &body, api.CIRequestMaxBytes) {
		return ci.CheckRetryRequest{}, false
	}
	if body.ExpectedRetryGeneration == nil || *body.ExpectedRetryGeneration < 0 || *body.ExpectedRetryGeneration > api.CICheckRetryMaxGenerations || !ci.ValidIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		api.WriteProblem(w, api.ErrValidation("Check retry requires expected_retry_generation and a bounded printable Idempotency-Key"))
		return ci.CheckRetryRequest{}, false
	}
	return ci.CheckRetryRequest{ExpectedRetryGeneration: *body.ExpectedRetryGeneration}, true
}

func (h Handler) RetryCheckDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.checkRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return
	}
	if !h.Config.GitHubAllows(acct.ID) || !h.Config.GitHubChecksEnabled || !h.Config.ExecutionChecksEnabled {
		httpkit.NotFound(w, "CI Check retry is not available")
		return
	}
	req, ok := checkRetryRequest(w, r)
	if !ok {
		return
	}
	row, replay, err := ci.RequestCheckRetry(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, r.PathValue("id"), r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.check.retry_requested", map[string]any{"retry_id": row.ID, "execution_id": row.ExecutionID, "generation": row.Generation})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, row)
}

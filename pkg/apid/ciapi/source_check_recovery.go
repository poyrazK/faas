package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) sourceCheckRecoveryStore(w http.ResponseWriter, acct state.Account) (ci.SourceCheckRecoveryStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.SourceCheckRecoveryStore)
	if !ok {
		httpkit.NotFound(w, "CI source Check delivery is not available")
	}
	return store, ok
}

func (h Handler) ListSourceCheckDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.sourceCheckRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) {
		return
	}
	rows, err := store.SourceCheckDeliveries(r.Context(), acct.ID, r.PathValue("binding"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": rows})
}

func (h Handler) SourceCheckDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.sourceCheckRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := store.SourceCheckDelivery(r.Context(), acct.ID, r.PathValue("binding"), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) RetrySourceCheckDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.sourceCheckRecoveryStore(w, acct)
	if !ok || !validID(w, r.PathValue("binding")) || !validID(w, r.PathValue("id")) {
		return
	}
	if !h.Config.GitHubAllows(acct.ID) || !h.Config.GitHubChecksEnabled || !h.Config.ExecutionChecksEnabled {
		httpkit.NotFound(w, "CI source Check retry is not available")
		return
	}
	req, ok := checkRetryRequest(w, r)
	if !ok {
		return
	}
	row, replay, err := ci.RequestSourceCheckRetry(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, r.PathValue("binding"), r.PathValue("id"), r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.source_check.retry_requested", map[string]any{"retry_id": row.ID, "binding_id": r.PathValue("binding"), "event_id": row.EventID, "generation": row.Generation})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, row)
}

package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) archiveStore(w http.ResponseWriter, acct state.Account) (ci.ArchiveStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.ArchiveStore)
	if !ok {
		httpkit.NotFound(w, "CI execution archives are not available")
	}
	return store, ok
}

func (h Handler) ArchiveExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.archiveStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return
	}
	var body struct{}
	if r.ContentLength != 0 && !httpkit.DecodeJSONLimit(w, r, &body, api.CIRequestMaxBytes) {
		return
	}
	row, replay, err := store.ArchiveExecution(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.execution.archived", map[string]any{"execution_id": row.ExecutionID, "status": row.Status})
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) ExecutionArchives(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.archiveStore(w, acct)
	if !ok {
		return
	}
	rows, err := store.ExecutionArchives(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"archives": rows})
}

func (h Handler) ExecutionArchive(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.archiveStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := store.ExecutionArchive(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

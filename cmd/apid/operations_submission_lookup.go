// adr: 601
package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) lookupPlatformTenantSelfOperationSubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	var req api.OperationSubmissionLookupRequest
	if !decodeOperationBody(w, r, &req, api.OperationSubmissionLookupMaxBytes) {
		return
	}
	store, ok := s.store.(state.OperationSubmissionLookupStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation submission lookup is unavailable"))
		return
	}
	result, err := store.LookupPlatformTenantOperationSubmission(r.Context(), acct.ID, tenant, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

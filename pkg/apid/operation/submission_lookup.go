// adr: 663
package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) LookupPlatformTenantSelfOperationSubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	var req api.OperationSubmissionLookupRequest
	if !DecodeOperationBody(w, r, &req, api.OperationSubmissionLookupMaxBytes) {
		return
	}
	store, ok := s.Host.Store().(state.OperationSubmissionLookupStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation submission lookup is unavailable"))
		return
	}
	result, err := store.LookupPlatformTenantOperationSubmission(r.Context(), acct.ID, tenant, req)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}

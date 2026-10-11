// adr: 662
package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) RecoverOperationWithReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.RawQuery != "" {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	var req api.OperationRecoveryRequest
	if !DecodeOperationBody(w, r, &req, op.ValueMaxBytes+api.OperationRecoveryBodyOverheadBytes) {
		return
	}
	store, ok := s.Host.Store().(state.OperationRecoveryReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation recovery receipts are unavailable"))
		return
	}
	decision, err := store.RecoverOperationWithReceipt(r.Context(), acct.ID, op.ID, req)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, decision)
}

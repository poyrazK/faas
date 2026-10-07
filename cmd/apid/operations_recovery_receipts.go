// adr: 600
package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) recoverOperationWithReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	var req api.OperationRecoveryRequest
	if !decodeOperationBody(w, r, &req, op.ValueMaxBytes+api.OperationRecoveryBodyOverheadBytes) {
		return
	}
	store, ok := s.store.(state.OperationRecoveryReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation recovery receipts are unavailable"))
		return
	}
	decision, err := store.RecoverOperationWithReceipt(r.Context(), acct.ID, op.ID, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decision)
}

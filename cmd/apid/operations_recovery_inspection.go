// adr: 641
package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) operationRecoveryReader(w http.ResponseWriter) (state.OperationRecoveryInspectionStore, bool) {
	reader, ok := s.store.(state.OperationRecoveryInspectionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation recovery inspection is unavailable"))
	}
	return reader, ok
}

func (s *server) inspectOperationRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	reader, ok := s.operationRecoveryReader(w)
	if !ok {
		return
	}
	inspection, err := reader.InspectOperationRecovery(r.Context(), acct.ID, op.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inspection)
}

func (s *server) previewOperationRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	reader, ok := s.operationRecoveryReader(w)
	if !ok {
		return
	}
	var request api.OperationRecoveryPreviewRequest
	if !decodeOperationBody(w, r, &request, op.ValueMaxBytes+api.OperationRecoveryBodyOverheadBytes) {
		return
	}
	preview, err := reader.PreviewOperationRecovery(r.Context(), acct.ID, op.ID, request)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

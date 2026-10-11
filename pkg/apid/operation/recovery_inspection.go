// adr: 660
package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) operationRecoveryReader(w http.ResponseWriter) (state.OperationRecoveryInspectionStore, bool) {
	reader, ok := s.Host.Store().(state.OperationRecoveryInspectionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation recovery inspection is unavailable"))
	}
	return reader, ok
}

func (s *Service) InspectOperationRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, inspection)
}

func (s *Service) PreviewOperationRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	reader, ok := s.operationRecoveryReader(w)
	if !ok {
		return
	}
	var request api.OperationRecoveryPreviewRequest
	if !DecodeOperationBody(w, r, &request, op.ValueMaxBytes+api.OperationRecoveryBodyOverheadBytes) {
		return
	}
	preview, err := reader.PreviewOperationRecovery(r.Context(), acct.ID, op.ID, request)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, preview)
}

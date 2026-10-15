// adr: 1197
package operation

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// SignalOperation delivers one named event to an account-owned Operation.
// A repeat of the same signal returns the original receipt.
func (s *Service) SignalOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationSignalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation signal store unavailable"))
		return
	}
	var req api.OperationSignalRequest
	if !DecodeOperationBody(w, r, &req, api.OperationSignalBodyMaxBytes) {
		return
	}
	receipt, err := store.SignalOperation(r.Context(), state.OperationSignalInput{AccountID: acct.ID, OperationID: op.ID, SignalID: req.SignalID, Event: req.Event, Payload: req.Payload})
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	s.emitSignalAudit(r, acct, receipt)
	httpkit.WriteJSON(w, http.StatusOK, receipt)
}

// SignalOperationsBySubject delivers one event to every active Operation of
// one name about one business subject, so callers need no Operation IDs.
// Operations that settle while the signal is delivered are skipped.
func (s *Service) SignalOperationsBySubject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationSignalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation signal store unavailable"))
		return
	}
	var req api.OperationSubjectSignalRequest
	if !DecodeOperationBody(w, r, &req, api.OperationSignalBodyMaxBytes) {
		return
	}
	if p := api.ValidateScope(req.Scope); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if len(req.Operation) == 0 || len(req.Operation) > api.OperationNameMaxBytes || api.ValidateOperationSubject(req.Subject) != nil {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	ids, err := store.OperationsForSubject(r.Context(), acct.ID, app.ID, req.Scope, req.Operation, req.Subject, api.OperationSignalSubjectTargetMax+1)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	if len(ids) > api.OperationSignalSubjectTargetMax {
		WriteOperationError(w, state.NewOperationLimitError("signal_subject_targets", api.OperationSignalSubjectTargetMax, int64(len(ids))))
		return
	}
	out := api.OperationSubjectSignalResponse{Delivered: []api.OperationSignalReceipt{}}
	for _, id := range ids {
		receipt, err := store.SignalOperation(r.Context(), state.OperationSignalInput{AccountID: acct.ID, OperationID: id, SignalID: req.SignalID, Event: req.Event, Payload: req.Payload})
		if errors.Is(err, state.ErrOperationNotActive) {
			continue
		}
		if err != nil {
			WriteOperationError(w, err)
			return
		}
		s.emitSignalAudit(r, acct, receipt)
		out.Delivered = append(out.Delivered, receipt)
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) emitSignalAudit(r *http.Request, acct state.Account, receipt api.OperationSignalReceipt) {
	if receipt.Duplicate {
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_signal_delivered", &acct.ID, map[string]any{"operation_id": receipt.OperationID, "signal_id": receipt.SignalID, "event": receipt.Event})
}

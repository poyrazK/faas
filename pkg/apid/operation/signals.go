// adr: 1197
package operation

import (
	"encoding/json"
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
	if ok {
		s.signalOne(w, r, acct, op, api.OperationSignalSourceAccount)
	}
}

// SignalPlatformTenantSelfOperation lets a customer deliver an event to its
// own Operation with its tenant credentials (ADR-1286).
func (s *Service) SignalPlatformTenantSelfOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.platformTenantSelfOperation(w, r, acct)
	if ok {
		s.signalOne(w, r, acct, op, api.OperationSignalSourceCustomer)
	}
}

func (s *Service) signalOne(w http.ResponseWriter, r *http.Request, acct state.Account, op state.Operation, source string) {
	store, ok := s.Host.Store().(state.OperationSignalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation signal store unavailable"))
		return
	}
	var req api.OperationSignalRequest
	if !DecodeOperationBody(w, r, &req, api.OperationSignalBodyMaxBytes) {
		return
	}
	if reservedSignalEvent(acct.ID, req.Event) {
		writeReservedSignalEvent(w)
		return
	}
	receipt, err := store.SignalOperation(r.Context(), state.OperationSignalInput{AccountID: acct.ID, OperationID: op.ID, SignalID: req.SignalID, Event: req.Event, Payload: req.Payload, Source: source})
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	s.emitSignalAudit(r, acct, receipt, source)
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
	var req api.OperationSubjectSignalRequest
	if !DecodeOperationBody(w, r, &req, api.OperationSignalBodyMaxBytes) {
		return
	}
	if p := api.ValidateScope(req.Scope); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if len(req.Operation) == 0 || len(req.Operation) > api.OperationNameMaxBytes {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	s.signalSubject(w, r, acct, subjectSignal{appID: app.ID, scope: req.Scope, operation: req.Operation, subject: req.Subject,
		signalID: req.SignalID, event: req.Event, payload: req.Payload, source: api.OperationSignalSourceAccount})
}

// SignalPlatformTenantSelfOperationsBySubject delivers one event to the
// customer's own active Operations of a definition about one subject. The
// definition fixes app, environment and name; other customers' Operations
// about the same subject are never reached (ADR-1286).
func (s *Service) SignalPlatformTenantSelfOperationsBySubject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.OperationStore(w)
	if !ok {
		return
	}
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	var req api.PlatformTenantSubjectSignalRequest
	if !DecodeOperationBody(w, r, &req, api.OperationSignalBodyMaxBytes) {
		return
	}
	def, err := store.OperationDefinitionByID(r.Context(), acct.ID, req.DefinitionID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	s.signalSubject(w, r, acct, subjectSignal{appID: def.AppID, tenantID: tenant, scope: def.Scope, operation: def.Spec.Name, subject: req.Subject,
		signalID: req.SignalID, event: req.Event, payload: req.Payload, source: api.OperationSignalSourceCustomer})
}

type subjectSignal struct {
	appID, tenantID, scope, operation string
	subject                           api.OperationSubject
	signalID, event, source           string
	payload                           json.RawMessage
}

func (s *Service) signalSubject(w http.ResponseWriter, r *http.Request, acct state.Account, in subjectSignal) {
	if reservedSignalEvent(acct.ID, in.event) {
		writeReservedSignalEvent(w)
		return
	}
	store, ok := s.Host.Store().(state.OperationSignalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation signal store unavailable"))
		return
	}
	if api.ValidateOperationSubject(in.subject) != nil {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	ids, err := store.OperationsForSubject(r.Context(), acct.ID, in.appID, in.tenantID, in.scope, in.operation, in.subject, api.OperationSignalSubjectTargetMax+1)
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
		receipt, err := store.SignalOperation(r.Context(), state.OperationSignalInput{AccountID: acct.ID, OperationID: id, SignalID: in.signalID, Event: in.event, Payload: in.payload, Source: in.source})
		if errors.Is(err, state.ErrOperationNotActive) {
			continue
		}
		if err != nil {
			WriteOperationError(w, err)
			return
		}
		s.emitSignalAudit(r, acct, receipt, in.source)
		out.Delivered = append(out.Delivered, receipt)
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) emitSignalAudit(r *http.Request, acct state.Account, receipt api.OperationSignalReceipt, source string) {
	if receipt.Duplicate {
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_signal_delivered", &acct.ID, map[string]any{"operation_id": receipt.OperationID, "signal_id": receipt.SignalID, "event": receipt.Event, "source": source})
}

// reservedSignalEvent reports whether only the platform may send the event:
// the children-settled event, for accounts with child Operations
// (ADR-20261018183000).
func reservedSignalEvent(accountID, event string) bool {
	return event == api.OperationChildrenSettledEvent && api.OperationChildrenEnabled(accountID)
}

func writeReservedSignalEvent(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "operation_signal_reserved", "Reserved event",
		"the platform sends operation.children.settled when a parent's children have settled"))
}

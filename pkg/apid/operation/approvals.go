// adr: 1194
package operation

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// DecideOperationApproval records the account's decision on a waiting
// operation whose definition names the account as approver.
func (s *Service) DecideOperationApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if ok {
		s.decideApproval(w, r, acct, op, "", api.OperationApproverAccount)
	}
}

// DecidePlatformTenantSelfOperationApproval records the customer's decision
// on its own waiting operation whose definition names the customer.
func (s *Service) DecidePlatformTenantSelfOperationApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.platformTenantSelfOperation(w, r, acct)
	if ok {
		s.decideApproval(w, r, acct, op, op.PlatformTenantID, api.OperationApproverCustomer)
	}
}

func (s *Service) decideApproval(w http.ResponseWriter, r *http.Request, acct state.Account, op state.Operation, tenant, kind string) {
	store, ok := s.Host.Store().(state.OperationApprovalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation approval store unavailable"))
		return
	}
	var req api.OperationApprovalRequest
	if !DecodeOperationBody(w, r, &req, api.OperationApprovalBodyMaxBytes) {
		return
	}
	actor := api.OperationApprovalActor{Kind: kind}
	if principal, ok := httpkit.PrincipalFrom(r); ok && principal.Key != nil {
		actor.ID = principal.Key.ID
	}
	got, err := store.DecideOperationApproval(r.Context(), state.OperationApprovalInput{AccountID: acct.ID, PlatformTenantID: tenant, OperationID: op.ID,
		Step: r.PathValue("step"), ApproverKind: kind, ExpectedGeneration: req.ExpectedGeneration, Decision: req.Decision, Comment: req.Comment, Actor: actor})
	if err != nil {
		writeOperationApprovalError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_approval_decided", &acct.ID, map[string]any{"operation_id": op.ID, "step": r.PathValue("step"), "decision": req.Decision, "approver": kind})
	httpkit.WriteJSON(w, http.StatusOK, got.OperationResponse)
}

func writeOperationApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrOperationApproverMismatch):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Not the approver", "this operation's definition names another approver for this step"))
	case errors.Is(err, state.ErrOperationApprovalDecided):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_approval_decided", "Approval already decided", "a different decision was already recorded for this step"))
	case errors.Is(err, state.ErrOperationNotAwaitingApproval):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_not_awaiting_approval", "Not waiting for this approval", "refresh the operation; it is not currently waiting on this step"))
	case errors.Is(err, state.ErrWorkflowCallbackExpired):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_approval_expired", "Approval expired", "the approval deadline passed; the operation settles as approval_expired"))
	default:
		WriteOperationError(w, err)
	}
}

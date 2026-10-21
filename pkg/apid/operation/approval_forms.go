// adr: 20261019001500
package operation

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// approvalFormStore returns the form store when approval input is open to
// the account. A closed account sees the routes as absent.
func (s *Service) approvalFormStore(w http.ResponseWriter, accountID string) (state.OperationApprovalFormStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !api.OperationApprovalInputsEnabled(accountID) {
		httpkit.NoSuchRoute(w)
		return nil, false
	}
	store, ok := s.Host.Store().(state.OperationApprovalFormStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("approval form storage is unavailable"))
	}
	return store, ok
}

func writeApprovalFormError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Approval form not found", "the form, Operation or approval step is unavailable"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("operation and step name an approval step, scope an environment, and input_schema is a JSON Schema object"))
	default:
		WriteOperationError(w, err)
	}
}

// PutOperationApprovalForm creates or replaces the form of one approval step.
func (s *Service) PutOperationApprovalForm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.approvalFormStore(w, acct.ID)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.OperationApprovalFormRequest
	if !DecodeOperationBody(w, r, &req, api.OperationDefinitionBodyMaxBytes) {
		return
	}
	form, created, err := store.PutOperationApprovalForm(r.Context(), state.OperationApprovalFormInput{AccountID: acct.ID, AppID: app.ID, Request: req, Now: time.Now()})
	if err != nil {
		writeApprovalFormError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_approval_form_set", &acct.ID, map[string]any{"form_id": form.ID, "app_id": app.ID,
		"scope": form.Scope, "operation": form.Operation, "step": form.Step, "required": form.Required})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpkit.WriteJSON(w, status, form)
}

func (s *Service) ListOperationApprovalForms(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.approvalFormStore(w, acct.ID)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	forms, err := store.OperationApprovalForms(r.Context(), acct.ID, app.ID)
	if err != nil {
		writeApprovalFormError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperationApprovalFormList{Forms: forms})
}

func (s *Service) DeleteOperationApprovalForm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.approvalFormStore(w, acct.ID)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := store.DeleteOperationApprovalForm(r.Context(), acct.ID, app.ID, r.PathValue("id")); err != nil {
		writeApprovalFormError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_approval_form_deleted", &acct.ID, map[string]any{"form_id": r.PathValue("id"), "app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

// GetOwnedOperationApprovalForm shows the account one Operation's approval
// form and, once decided, the recorded decision.
func (s *Service) GetOwnedOperationApprovalForm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.approvalFormStore(w, acct.ID)
	if !ok {
		return
	}
	op, _, ok := s.ownedOperation(w, r, acct)
	if ok {
		s.writeApprovalFormView(w, r, store, acct.ID, "", op.ID)
	}
}

// GetPlatformTenantSelfOperationApprovalForm shows a customer the form of an
// approval it decides on its own Operation.
func (s *Service) GetPlatformTenantSelfOperationApprovalForm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.approvalFormStore(w, acct.ID)
	if !ok {
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if ok {
		s.writeApprovalFormView(w, r, store, acct.ID, tenant, r.PathValue("id"))
	}
}

func (s *Service) writeApprovalFormView(w http.ResponseWriter, r *http.Request, store state.OperationApprovalFormStore, accountID, tenantID, operationID string) {
	view, err := store.OperationApprovalFormView(r.Context(), accountID, tenantID, operationID, r.PathValue("step"))
	if err != nil {
		writeApprovalFormError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, view)
}

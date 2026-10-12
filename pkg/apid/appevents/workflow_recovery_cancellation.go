package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) PreviewWorkflowRecoveryCancellation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.workflowRecoveryCancellation(w, r, acct, true)
}
func (s *Service) CancelWorkflowRecoveryExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.workflowRecoveryCancellation(w, r, acct, false)
}
func (s *Service) workflowRecoveryCancellation(w http.ResponseWriter, r *http.Request, acct state.Account, preview bool) {
	store, ok := s.Host.Store().(state.WorkflowRecoveryCancellationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("recovery cancellation unavailable"))
		return
	}
	var req api.WorkflowRecoveryCancellationRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid recovery cancellation request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	r = recoveryActorRequest(r)
	out, err := store.WorkflowRecoveryCancellation(r.Context(), acct.ID, r.PathValue("jobID"), req, preview)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func (s *Service) GetWorkflowRecoveryActionReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.Host.Store().(state.WorkflowRecoveryActionReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery receipts unavailable"))
		return
	}
	out, err := store.GetWorkflowRecoveryActionReceipt(r.Context(), acct.ID, r.PathValue("jobID"), r.PathValue("requestID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) GetEventRecoveryByRequest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.Host.Store().(state.WorkflowRecoveryActionReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery receipts unavailable"))
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := store.GetEventRecoveryByRequest(r.Context(), acct.ID, app.ID, r.PathValue("requestID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

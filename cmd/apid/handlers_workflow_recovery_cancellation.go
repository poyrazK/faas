package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) previewWorkflowRecoveryCancellation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.workflowRecoveryCancellation(w, r, acct, true)
}
func (s *server) cancelWorkflowRecoveryExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.workflowRecoveryCancellation(w, r, acct, false)
}
func (s *server) workflowRecoveryCancellation(w http.ResponseWriter, r *http.Request, acct state.Account, preview bool) {
	store, ok := s.store.(state.WorkflowRecoveryCancellationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("recovery cancellation unavailable"))
		return
	}
	var req api.WorkflowRecoveryCancellationRequest
	if err := decodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
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

func (s *server) getWorkflowRecoveryActionReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.WorkflowRecoveryActionReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery receipts unavailable"))
		return
	}
	out, err := store.GetWorkflowRecoveryActionReceipt(r.Context(), acct.ID, r.PathValue("jobID"), r.PathValue("requestID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) getEventRecoveryByRequest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.WorkflowRecoveryActionReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery receipts unavailable"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := store.GetEventRecoveryByRequest(r.Context(), acct.ID, app.ID, r.PathValue("requestID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) retryEvidenceScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.WorkflowRetryEvidenceStore, string, bool) {
	_, app, ok := s.initialReviewScope(w, r, acct)
	if !ok {
		return nil, "", false
	}
	store, ok := s.Host.Store().(state.WorkflowRetryEvidenceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("retry evidence store"))
		return nil, "", false
	}
	return store, app, true
}
func (s *Service) DiagnoseWorkflowRetryEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.retryEvidenceScope(w, r, acct)
	if !ok {
		return
	}
	out, err := store.DiagnoseWorkflowRetryEvidence(r.Context(), acct.ID, app, r.PathValue("reviewID"), r.PathValue("actionID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) RepairWorkflowRetryEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.retryEvidenceScope(w, r, acct)
	if !ok {
		return
	}
	var req api.WorkflowInitialEvidenceRepairRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid retry evidence repair request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := store.RepairWorkflowRetryEvidence(recoveryActorRequest(r).Context(), acct.ID, app, r.PathValue("reviewID"), r.PathValue("actionID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

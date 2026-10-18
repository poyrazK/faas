package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) retryParentEvidenceScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.WorkflowRetryParentEvidenceStore, string, bool) {
	_, app, ok := s.initialReviewScope(w, r, acct)
	if !ok {
		return nil, "", false
	}
	store, ok := s.Host.Store().(state.WorkflowRetryParentEvidenceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("retry parent evidence store"))
		return nil, "", false
	}
	return store, app, true
}
func (s *Service) DiagnoseWorkflowRetryParentEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.retryParentEvidenceScope(w, r, acct)
	if !ok {
		return
	}
	out, err := store.DiagnoseWorkflowRetryParentEvidence(r.Context(), acct.ID, app, r.PathValue("reviewID"), r.PathValue("actionID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) RepairWorkflowRetryParentEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.retryParentEvidenceScope(w, r, acct)
	if !ok {
		return
	}
	var req api.WorkflowInitialEvidenceRepairRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid parent repair request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := store.RepairWorkflowRetryParentEvidence(recoveryActorRequest(r).Context(), acct.ID, app, r.PathValue("reviewID"), r.PathValue("actionID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

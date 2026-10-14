package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) initialEvidenceScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.WorkflowInitialEvidenceStore, string, bool) {
	_, app, ok := s.initialReviewScope(w, r, acct)
	if !ok {
		return nil, "", false
	}
	store, ok := s.Host.Store().(state.WorkflowInitialEvidenceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("initial evidence store"))
		return nil, "", false
	}
	return store, app, true
}
func (s *Service) DiagnoseWorkflowInitialEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.initialEvidenceScope(w, r, acct)
	if !ok {
		return
	}
	out, err := store.DiagnoseWorkflowInitialEvidence(r.Context(), acct.ID, app, r.PathValue("reviewID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) RepairWorkflowInitialEvidence(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.initialEvidenceScope(w, r, acct)
	if !ok {
		return
	}
	var req api.WorkflowInitialEvidenceRepairRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid evidence repair request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := store.RepairWorkflowInitialEvidence(recoveryActorRequest(r).Context(), acct.ID, app, r.PathValue("reviewID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

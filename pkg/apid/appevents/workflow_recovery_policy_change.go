package appevents

import (
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) PreviewWorkflowRecoveryPolicyChange(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	preview, ok := store.(state.WorkflowRecoveryPolicyChangeStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("policy change preview unavailable"))
		return
	}
	var req api.SetWorkflowRecoveryPolicyRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid policy change preview request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := preview.PreviewWorkflowRecoveryPolicyChange(r.Context(), acct.ID, app, req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

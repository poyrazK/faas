package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) EvaluateWorkflowRecoveryPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	evaluation, ok := store.(state.WorkflowRecoveryPolicyEvaluationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("historical policy evaluation unavailable"))
		return
	}
	var req api.WorkflowRecoveryPolicyEvaluationRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid historical policy evaluation request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := evaluation.EvaluateWorkflowRecoveryPolicy(r.Context(), acct.ID, app, req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

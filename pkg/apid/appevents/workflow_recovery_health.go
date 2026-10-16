package appevents

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetWorkflowRecoveryPolicyHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	operator, ok := store.(state.WorkflowRecoveryPolicyOperatorStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("automatic queue unavailable"))
		return
	}
	seconds := api.WorkflowRecoveryHealthDefaultStallSeconds
	if values, exists := r.URL.Query()["stall_after_seconds"]; exists {
		var err error
		if len(values) != 1 {
			api.WriteProblem(w, api.ErrValidation("provide one stall_after_seconds"))
			return
		}
		seconds, err = strconv.Atoi(values[0])
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid stall_after_seconds"))
			return
		}
	}
	if err := api.ValidateRecoveryHealthThreshold(seconds); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if overview, supported := store.(state.WorkflowRecoveryHealthOverviewStore); supported {
		health, err := overview.GetWorkflowRecoveryPolicyHealth(r.Context(), acct.ID, app, seconds)
		s.writeEventRecovery(w, r, http.StatusOK, health, err)
		return
	}
	queue, err := operator.GetWorkflowRecoveryAutomaticQueue(r.Context(), acct.ID, app)
	s.writeEventRecovery(w, r, http.StatusOK, api.RecoveryPolicyHealth(queue, seconds), err)
}

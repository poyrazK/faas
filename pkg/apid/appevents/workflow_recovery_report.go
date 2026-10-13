package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) GetWorkflowRecoveryReport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	reports, ok := store.(state.WorkflowRecoveryReportStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery report unavailable"))
		return
	}
	q := r.URL.Query()
	for name := range q {
		switch name {
		case "workflow", "policy_version", "created_after", "created_before":
		default:
			api.WriteProblem(w, api.ErrValidation("unsupported recovery report filter"))
			return
		}
	}
	o, err := recoveryHistoryOptions(q)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid recovery report filters"))
		return
	}
	out, err := reports.GetWorkflowRecoveryReport(r.Context(), acct.ID, app, api.WorkflowRecoveryReportOptions{Workflow: o.Workflow, PolicyVersion: o.PolicyVersion, CreatedAfter: o.CreatedAfter, CreatedBefore: o.CreatedBefore})
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

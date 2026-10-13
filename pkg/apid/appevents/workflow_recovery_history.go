package appevents

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryHistoryOptions(q url.Values) (api.WorkflowRecoveryHistoryOptions, error) {
	o := api.WorkflowRecoveryHistoryOptions{Workflow: q.Get("workflow"), RunID: q.Get("run_id"), After: q.Get("after")}
	for _, name := range []string{"workflow", "run_id", "after", "limit", "policy_version", "created_after", "created_before"} {
		if len(q[name]) > 1 {
			return o, strconv.ErrSyntax
		}
	}
	var err error
	if value, ok := q["limit"]; ok {
		o.Limit, err = strconv.Atoi(value[0])
		if err != nil {
			return o, err
		}
	}
	if value, ok := q["policy_version"]; ok {
		o.PolicyVersion, err = strconv.ParseInt(value[0], 10, 64)
		if err != nil {
			return o, err
		}
	}
	if value, ok := q["created_after"]; ok {
		o.CreatedAfter, err = time.Parse(time.RFC3339Nano, value[0])
		if err != nil {
			return o, err
		}
	}
	if value, ok := q["created_before"]; ok {
		o.CreatedBefore, err = time.Parse(time.RFC3339Nano, value[0])
		if err != nil {
			return o, err
		}
	}
	return o, o.Validate()
}
func (s *Service) GetWorkflowRecoveryHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	history, ok := store.(state.WorkflowRecoveryHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery history unavailable"))
		return
	}
	o, err := recoveryHistoryOptions(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid recovery history filters"))
		return
	}
	out, err := history.GetWorkflowRecoveryHistory(r.Context(), acct.ID, app, o)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

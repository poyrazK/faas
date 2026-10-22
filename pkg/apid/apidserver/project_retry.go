package apidserver

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) prepareProjectRetry(r *http.Request, acct state.Account, req *scanPlanRequest) *api.Problem {
	value := r.Header.Get(api.ProjectRetryHeader)
	if value == "" {
		return nil
	}
	if len(value) > 32768 || json.Unmarshal([]byte(value), &req.RetryTargets) != nil || len(req.RetryTargets) == 0 {
		return api.ErrValidation("invalid project retry targets")
	}
	if !api.ValidDeploymentAttemptID(r.Header.Get(api.DeploymentAttemptHeader)) || !req.NoTriggers || len(req.Secrets) > 0 || len(req.InitialSecrets) > 0 || len(req.Exclude) > 0 || req.PersistExclude {
		return api.ErrValidation("project retry requires a fresh tracked attempt, --only and --no-triggers, without secret or exclusion changes")
	}
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug)
	if err != nil {
		return api.ErrValidation("retry project no longer exists")
	}
	if err := deploystaging.ValidateProjectRetry(r.Context(), s.store, acct.ID, project.ID, req.Environment, req.Only, req.RetryTargets); err != nil {
		return api.NewProblem(http.StatusConflict, "project_retry_changed", "Project retry stopped", "A workload was replaced, is no longer failed, or has a newer deployment. Inspect deployment history before submitting another attempt.")
	}
	return nil
}

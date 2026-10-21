package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Report mode is always available. Enforce mode needs the enforce worker
// running and the environment_gitops_execution platform flag to admit the
// account (ADR-20261021080847); reserving fields without an executor would
// also remove them from reporting, so customer controls fail closed.
func (s *server) rejectEnvironmentGitEnforcement(w http.ResponseWriter, acct state.Account, mode string) bool {
	if mode != "enforce" || s.environmentGitEnforcementAvailable(acct) {
		return false
	}
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_git_enforcement_unavailable", "Environment enforcement unavailable", "GitOps execution is not enabled for this account. Report mode records drift without applying changes."))
	return true
}

// environmentGitEnforcementAvailable reports whether this account may select
// enforce mode now.
func (s *server) environmentGitEnforcementAvailable(acct state.Account) bool {
	return s.environmentGitOpsEnforcementWorkerLive.Load() && s.platformFlagEnabled(platformflags.EnvironmentGitOpsExecution, acct)
}

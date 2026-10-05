package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// The preview has a report worker only. Reserving fields without an executor
// would also remove them from reporting, so customer controls fail closed.
func rejectEnvironmentGitEnforcement(w http.ResponseWriter, mode string) bool {
	if mode != "enforce" {
		return false
	}
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_git_enforcement_unavailable", "Environment enforcement unavailable", "This preview supports report mode. Continuous enforcement is not available yet."))
	return true
}

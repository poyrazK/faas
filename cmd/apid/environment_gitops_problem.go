package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func writeEnvironmentGitOpsOwnershipProblem(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, state.ErrEnvironmentGitManaged) {
		return false
	}
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_field_git_managed", "Setting managed by Git",
		"Change the environment definition in Git, or create a temporary override with a reason and expiry before editing this setting."))
	return true
}

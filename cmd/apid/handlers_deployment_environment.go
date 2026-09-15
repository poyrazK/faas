package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyDeploymentEnvironment resolves the customer-facing environment name
// against the project's durable registry and converts it to the existing
// deployment scope field. Raw Scope callers remain untouched for backwards
// compatibility; only the new Environment field is registry-backed.
func (s *server) applyDeploymentEnvironment(ctx context.Context, acct state.Account, app state.App, req *api.CreateDeploymentRequest) *api.Problem {
	environment := strings.TrimSpace(req.Environment)
	if environment == "" {
		return nil
	}
	if req.Scope != "" {
		return api.ErrValidation("environment and scope are mutually exclusive")
	}
	if !api.ValidProjectEnvironmentSlug(environment) {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment", "environment must be a lowercase project environment slug")
	}
	if problem := api.ValidateScope(environment); problem != nil {
		return problem
	}
	if app.ProjectID == "" {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Environment unavailable", "--environment requires an app attached to a project")
	}
	env, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, app.ProjectID, environment)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Project environment not found",
				"no environment "+environment+" exists in this project")
		}
		return api.ErrCapacity("could not load project environment")
	}
	req.Scope = env.Slug
	return nil
}

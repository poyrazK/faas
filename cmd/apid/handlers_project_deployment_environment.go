package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateProjectDeploymentEnvironment checks the registry before a project
// scan or apply. A new project may target its implicitly-seeded production
// environment; non-production environments must already be registered on an
// existing project so a read-only scan never creates platform state.
func (s *server) validateProjectDeploymentEnvironment(ctx context.Context, acct state.Account, projectSlug, environment string) *api.Problem {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return nil
	}
	if !api.ValidProjectEnvironmentSlug(environment) {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment", "environment must be a lowercase project environment slug")
	}
	if problem := api.ValidateScope(environment); problem != nil {
		return problem
	}
	project, err := s.store.ProjectBySlug(ctx, acct.ID, projectSlug)
	if errors.Is(err, state.ErrNotFound) {
		if environment == "production" {
			return nil
		}
		return projectEnvironmentNotFound(projectSlug, environment)
	}
	if err != nil {
		return api.ErrCapacity("could not load project environment")
	}
	if _, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, environment); errors.Is(err, state.ErrNotFound) {
		return projectEnvironmentNotFound(projectSlug, environment)
	} else if err != nil {
		return api.ErrCapacity("could not load project environment")
	}
	return nil
}

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
	_, problem := s.projectDeploymentEnvironmentProtection(ctx, acct, projectSlug, environment)
	return problem
}

// projectDeploymentEnvironmentProtection validates the deployment target and
// returns whether applying to it requires an exact-plan approval. Production
// is implicitly protected for a brand-new project, matching project creation's
// default environment without making a read-only scan create state.
func (s *server) projectDeploymentEnvironmentProtection(ctx context.Context, acct state.Account, projectSlug, environment string) (bool, *api.Problem) {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return false, nil
	}
	if !api.ValidProjectEnvironmentSlug(environment) {
		return false, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment", "environment must be a lowercase project environment slug")
	}
	if problem := api.ValidateScope(environment); problem != nil {
		return false, problem
	}
	project, err := s.store.ProjectBySlug(ctx, acct.ID, projectSlug)
	if errors.Is(err, state.ErrNotFound) {
		if environment == "production" {
			return true, nil
		}
		return false, projectEnvironmentNotFound(projectSlug, environment)
	}
	if err != nil {
		return false, api.ErrCapacity("could not load project environment")
	}
	env, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return false, projectEnvironmentNotFound(projectSlug, environment)
	} else if err != nil {
		return false, api.ErrCapacity("could not load project environment")
	}
	return env.Protected, nil
}

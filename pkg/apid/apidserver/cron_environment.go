package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronEnvironmentScope resolves the environment a new cron runs in
// (ADR-20261104120000). Production names resolve to the empty scope every
// existing cron has. A stage needs a project app with that environment and,
// until stage HTTP crons ship, a deployment command.
func (s *server) cronEnvironmentScope(ctx context.Context, app state.App, environment string, command bool) (string, *api.Problem) {
	scope := state.NormalizeCronDeploymentScope(environment)
	if scope == "" {
		return "", nil
	}
	if !state.ValidCronDeploymentScope(scope) {
		return "", api.ErrValidation("environment must be a project environment slug")
	}
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		return "", api.ErrCronEnvironmentRequiresProject()
	}
	env, err := s.store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope)
	if errors.Is(err, state.ErrNotFound) {
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such environment "+scope+" in this app's project")
	}
	if err != nil || env.ProjectID != app.ProjectID {
		return "", api.ErrCapacity("could not resolve the cron environment")
	}
	if !command {
		return "", api.ErrCronEnvironmentHTTPUnavailable(scope)
	}
	return scope, nil
}

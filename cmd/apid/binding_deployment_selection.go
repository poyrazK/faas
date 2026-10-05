// adr: 428 — only platform binding probes may select a live deployment explicitly.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) selectBindingVerificationDeployment(ctx context.Context, app state.App, id string) (state.Deployment, *api.Problem) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return state.Deployment{}, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid deployment selector", "binding verification deployment must be a deployment UUID")
	}
	deployment, err := s.store.DeploymentByID(ctx, parsed.String())
	// MemStore retains historical 32-hex IDs; PostgreSQL returns dashed IDs.
	if errors.Is(err, state.ErrNotFound) {
		deployment, err = s.store.DeploymentByID(ctx, strings.ReplaceAll(parsed.String(), "-", ""))
	}
	if errors.Is(err, state.ErrNotFound) || err == nil && deployment.AppID != app.ID {
		return state.Deployment{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment for this app.")
	}
	if err != nil {
		return state.Deployment{}, api.ErrInternal("could not read binding verification deployment")
	}
	if deployment.Status != state.DeployLive || deployment.RootfsKey == "" || deployment.ImageDigest == "" {
		return state.Deployment{}, appTaskDeploymentUnavailableProblem()
	}
	return deployment, nil
}

func (s *server) selectAppTaskDeployment(ctx context.Context, app state.App, resolved api.ResolvedCreateAppTaskRequest) (state.Deployment, *api.Problem) {
	if resolved.VerificationDeploymentID != "" {
		return s.selectBindingVerificationDeployment(ctx, app, resolved.VerificationDeploymentID)
	}
	deployment, err := s.store.LiveDeployment(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return state.Deployment{}, appTaskDeploymentUnavailableProblem()
	}
	if err != nil {
		return state.Deployment{}, api.ErrInternal("could not select the app task deployment")
	}
	return deployment, nil
}

// Package deploymentcheck validates the immutable target shared by apid and
// gatewayd's owner-authorized deployment response check.
package deploymentcheck

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	AppByID(context.Context, string) (state.App, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	ProjectEnvironmentBySlug(context.Context, string, string, string) (state.ProjectEnvironment, error)
}

func Resolve(ctx context.Context, store Store, request api.DeploymentResponseCheckRequest, deploymentID string) (state.App, state.Deployment, error) {
	app, err := store.AppByID(ctx, request.AppID)
	if err != nil {
		return app, state.Deployment{}, fmt.Errorf("read response-check app: %w", err)
	}
	if app.Status == state.AppDeleted || !app.AcceptsRequestInvocations() || request.ProjectID != "" && app.ProjectID != request.ProjectID {
		return app, state.Deployment{}, state.ErrNotFound
	}
	dep, err := store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return app, dep, fmt.Errorf("read response-check deployment: %w", err)
	}
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if dep.AppID != app.ID || dep.Status != state.DeployLive || scope != request.Scope {
		return app, dep, state.ErrNotFound
	}
	if request.EnvironmentID != "" || request.Scope != api.DefaultEnvScope {
		if app.ProjectID == "" || app.PreviewOfSlug != "" {
			return app, dep, state.ErrNotFound
		}
		env, err := store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, request.Scope)
		if err != nil {
			return app, dep, fmt.Errorf("read response-check environment: %w", err)
		}
		if env.ID != request.EnvironmentID || env.ProjectID != app.ProjectID || env.AccountID != app.AccountID || env.Slug != request.Scope {
			return app, dep, state.ErrNotFound
		}
	}
	return app, dep, nil
}

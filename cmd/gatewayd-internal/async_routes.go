package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

var asyncRouteInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-invocation/v1"))
var asyncRouteEnvironmentInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-environment-invocation/v1"))

type asyncRouteInvocationStore interface {
	AppByID(context.Context, string) (state.App, error)
	EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	InvocationByID(context.Context, string) (state.Invocation, error)
}

type asyncRouteEnqueuer struct {
	store asyncRouteInvocationStore
}

func (e *asyncRouteEnqueuer) EnqueueAsyncRoute(ctx context.Context, req gateway.AsyncRouteRequest) (gateway.AsyncRouteAccepted, error) {
	app, err := e.store.AppByID(ctx, req.AppID)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	if req.AccountID == "" || app.AccountID != req.AccountID {
		return gateway.AsyncRouteAccepted{}, state.ErrNotFound
	}
	headers, err := json.Marshal(req.Headers)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	prepared, version, err := state.ResolveInvocationVersionForEnvironment(ctx, e.store, state.Invocation{
		AppID:                  req.AppID,
		AccountID:              req.AccountID,
		Source:                 state.InvocationAsyncInvoke,
		Method:                 req.Method,
		Path:                   req.Path,
		Payload:                append(json.RawMessage(nil), req.Payload...),
		Headers:                headers,
		DueAt:                  time.Now().UTC(),
		DeadlineAt:             req.DeadlineAt,
		OnSuccessDestinationID: req.OnSuccessWebhook,
		OnFailureDestinationID: req.OnFailureWebhook,
	}, req.Scope)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	app, err = e.appForVersion(ctx, app, version)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	prepared.RetryPolicyJSON, err = asyncRouteRetryPolicy(app, req.RetryPolicy)
	if err != nil {
		return gateway.AsyncRouteAccepted{}, err
	}
	invocationID := asyncRouteInvocationID(req.AppID, version.Scope, req.IdempotencyKey)
	prepared.ID = invocationID
	inv, err := e.store.EnqueueInvocation(ctx, prepared)
	if err == nil {
		return gateway.AsyncRouteAccepted{ID: inv.ID, ReleaseID: version.ReleaseID, DeploymentID: version.DeploymentID}, nil
	}
	if !errors.Is(err, state.ErrConflict) || invocationID == "" {
		return gateway.AsyncRouteAccepted{}, err
	}
	existing, lookupErr := e.store.InvocationByID(ctx, invocationID)
	if lookupErr != nil || existing.AppID != req.AppID || existing.AccountID != req.AccountID {
		if lookupErr != nil {
			return gateway.AsyncRouteAccepted{}, lookupErr
		}
		return gateway.AsyncRouteAccepted{}, state.ErrConflict
	}
	_, existingVersion, versionErr := state.ResolveInvocationVersion(ctx, e.store, existing)
	if versionErr != nil {
		return gateway.AsyncRouteAccepted{}, versionErr
	}
	if asyncRouteScopeKey(existingVersion.Scope) != asyncRouteScopeKey(version.Scope) {
		return gateway.AsyncRouteAccepted{}, state.ErrConflict
	}
	return gateway.AsyncRouteAccepted{ID: existing.ID, ReleaseID: existingVersion.ReleaseID, DeploymentID: existingVersion.DeploymentID}, nil
}

func asyncRouteInvocationID(appID, scope, key string) string {
	if key == "" {
		return ""
	}
	scope = asyncRouteScopeKey(scope)
	if scope == "production" {
		// Preserve committed default-environment receipts across the upgrade.
		return uuid.NewSHA1(asyncRouteInvocationNamespace, []byte(appID+"\x00"+key)).String()
	}
	return uuid.NewSHA1(asyncRouteEnvironmentInvocationNamespace, []byte(appID+"\x00"+scope+"\x00"+key)).String()
}

func asyncRouteScopeKey(scope string) string {
	if scope == state.DefaultEnvScope {
		return "production"
	}
	return scope
}

func (e *asyncRouteEnqueuer) appForVersion(ctx context.Context, app state.App, version state.InvocationVersion) (state.App, error) {
	if version.Scope == "production" || version.Scope == state.DefaultEnvScope {
		return app, nil
	}
	reader, ok := e.store.(interface {
		DeploymentByID(context.Context, string) (state.Deployment, error)
	})
	if !ok || version.DeploymentID == "" {
		return state.App{}, state.ErrConflict
	}
	dep, err := reader.DeploymentByID(ctx, version.DeploymentID)
	if err != nil {
		return state.App{}, err
	}
	if dep.AppID != app.ID || dep.Scope != version.Scope || dep.Status != state.DeployLive {
		return state.App{}, state.ErrConflict
	}
	effective, err := state.AppForDeployment(ctx, e.store, dep)
	if err == nil && (effective.ID != app.ID || effective.AccountID != app.AccountID || effective.ProjectID != app.ProjectID || !effective.AcceptsRequestInvocations()) {
		return state.App{}, state.ErrConflict
	}
	return effective, err
}

func asyncRouteRetryPolicy(app state.App, override *api.RetryPolicyDTO) (json.RawMessage, error) {
	if override != nil {
		return json.Marshal(override)
	}
	if string(app.RetryPolicyJSON) == "{}" {
		return nil, nil
	}
	return append(json.RawMessage(nil), app.RetryPolicyJSON...), nil
}

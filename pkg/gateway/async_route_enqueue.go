package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var asyncRouteInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-invocation/v1"))
var asyncRouteEnvironmentInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-environment-invocation/v1"))
var asyncRouteEnvironmentTenantInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-environment-tenant-invocation/v1"))

type AsyncRouteInvocationStore interface {
	AppByID(context.Context, string) (state.App, error)
	EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	InvocationByID(context.Context, string) (state.Invocation, error)
}

// EnqueueAsyncRoute persists trusted ingress identity separately from customer headers.
func EnqueueAsyncRoute(ctx context.Context, store AsyncRouteInvocationStore, req AsyncRouteRequest) (AsyncRouteAccepted, error) {
	app, err := store.AppByID(ctx, req.AppID)
	if err != nil {
		return AsyncRouteAccepted{}, err
	}
	if req.AccountID == "" || app.AccountID != req.AccountID {
		return AsyncRouteAccepted{}, state.ErrNotFound
	}
	headers, err := json.Marshal(req.Headers)
	if err != nil {
		return AsyncRouteAccepted{}, err
	}
	prepared, version, err := state.ResolveInvocationVersionForEnvironment(ctx, store, state.Invocation{
		AppID:                  req.AppID,
		AccountID:              req.AccountID,
		PlatformTenantID:       req.PlatformTenantID,
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
		return AsyncRouteAccepted{}, err
	}
	app, err = asyncRouteAppForVersion(ctx, store, app, version)
	if err != nil {
		return AsyncRouteAccepted{}, err
	}
	prepared.RetryPolicyJSON, err = asyncRouteRetryPolicy(app, req.RetryPolicy)
	if err != nil {
		return AsyncRouteAccepted{}, err
	}
	invocationID := asyncRouteInvocationID(req.AppID, version.Scope, req.PlatformTenantID, req.IdempotencyKey)
	prepared.ID = invocationID
	inv, err := store.EnqueueInvocation(ctx, prepared)
	if err == nil {
		return AsyncRouteAccepted{ID: inv.ID, ReleaseID: version.ReleaseID, DeploymentID: version.DeploymentID}, nil
	}
	if !errors.Is(err, state.ErrConflict) || invocationID == "" {
		return AsyncRouteAccepted{}, err
	}
	existing, lookupErr := store.InvocationByID(ctx, invocationID)
	if lookupErr != nil || existing.AppID != req.AppID || existing.AccountID != req.AccountID || existing.PlatformTenantID != req.PlatformTenantID {
		if lookupErr != nil {
			return AsyncRouteAccepted{}, lookupErr
		}
		return AsyncRouteAccepted{}, state.ErrConflict
	}
	_, existingVersion, versionErr := state.ResolveInvocationVersion(ctx, store, existing)
	if versionErr != nil {
		return AsyncRouteAccepted{}, versionErr
	}
	if asyncRouteScopeKey(existingVersion.Scope) != asyncRouteScopeKey(version.Scope) {
		return AsyncRouteAccepted{}, state.ErrConflict
	}
	return AsyncRouteAccepted{ID: existing.ID, ReleaseID: existingVersion.ReleaseID, DeploymentID: existingVersion.DeploymentID}, nil
}

// Preserve committed production IDs and distinguish stages and customer scopes.
func asyncRouteInvocationID(appID, scope, tenantID, key string) string {
	if key == "" {
		return ""
	}
	scope = asyncRouteScopeKey(scope)
	if scope == "production" {
		receiptKey := appID + "\x00" + key
		if tenantID != "" {
			receiptKey = appID + "\x00tenant\x00" + tenantID + "\x00" + key
		}
		return uuid.NewSHA1(asyncRouteInvocationNamespace, []byte(receiptKey)).String()
	}
	if tenantID != "" {
		return uuid.NewSHA1(asyncRouteEnvironmentTenantInvocationNamespace, []byte(appID+"\x00"+scope+"\x00"+tenantID+"\x00"+key)).String()
	}
	return uuid.NewSHA1(asyncRouteEnvironmentInvocationNamespace, []byte(appID+"\x00"+scope+"\x00"+key)).String()
}

func asyncRouteScopeKey(scope string) string {
	if scope == state.DefaultEnvScope {
		return "production"
	}
	return scope
}

func asyncRouteAppForVersion(ctx context.Context, store AsyncRouteInvocationStore, app state.App, version state.InvocationVersion) (state.App, error) {
	if version.Scope == "production" || version.Scope == state.DefaultEnvScope {
		return app, nil
	}
	reader, ok := store.(interface {
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
	effective, err := state.AppForDeployment(ctx, store, dep)
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

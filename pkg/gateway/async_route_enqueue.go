package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

var asyncRouteInvocationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/async-route-invocation/v1"))

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
	invocationID := ""
	if req.IdempotencyKey != "" {
		key := req.AppID + "\x00" + req.IdempotencyKey
		if req.PlatformTenantID != "" {
			key = req.AppID + "\x00tenant\x00" + req.PlatformTenantID + "\x00" + req.IdempotencyKey
		}
		invocationID = uuid.NewSHA1(asyncRouteInvocationNamespace, []byte(key)).String()
	}
	retryPolicy := app.RetryPolicyJSON
	if req.RetryPolicy != nil {
		retryPolicy, err = json.Marshal(req.RetryPolicy)
		if err != nil {
			return AsyncRouteAccepted{}, err
		}
	}
	if string(retryPolicy) == "{}" {
		retryPolicy = nil
	}
	prepared, version, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{
		ID:                     invocationID,
		AppID:                  req.AppID,
		AccountID:              req.AccountID,
		PlatformTenantID:       req.PlatformTenantID,
		Source:                 state.InvocationAsyncInvoke,
		Method:                 req.Method,
		Path:                   req.Path,
		Payload:                append(json.RawMessage(nil), req.Payload...),
		Headers:                headers,
		DueAt:                  time.Now().UTC(),
		RetryPolicyJSON:        append(json.RawMessage(nil), retryPolicy...),
		DeadlineAt:             req.DeadlineAt,
		OnSuccessDestinationID: req.OnSuccessWebhook,
		OnFailureDestinationID: req.OnFailureWebhook,
	})
	if err != nil {
		return AsyncRouteAccepted{}, err
	}
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
	return AsyncRouteAccepted{ID: existing.ID, ReleaseID: existingVersion.ReleaseID, DeploymentID: existingVersion.DeploymentID}, nil
}

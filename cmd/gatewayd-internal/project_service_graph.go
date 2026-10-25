package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func resolveFrozenProjectBinding(ctx context.Context, store state.Store, caller, deployment, service string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	if reader, ok := store.(state.ProjectServiceGraphStore); ok {
		return reader.ResolveProjectServiceBinding(ctx, caller, deployment, service, gateway.RequestedServiceRelease(ctx))
	}
	return state.EnvironmentGitOpsServiceBindingRoute{}, false, false, nil
}

// Frozen binding resolution has already checked the exact graph and its
// policies. Repeat the live tenant boundary; mutable App policy must not
// replace the retained deployment's frozen policy.
func newServiceProxyBoundAuthorizer(store state.Store) gateway.ServiceProxyAuthorizer {
	return func(ctx context.Context, callerID, targetID string) (gateway.ServiceCaller, error) {
		if !isAppID(callerID) || !isAppID(targetID) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		caller, err := store.AppByID(ctx, callerID)
		if err != nil {
			return gateway.ServiceCaller{}, err
		}
		target, err := store.AppByID(ctx, targetID)
		if err != nil {
			return gateway.ServiceCaller{}, err
		}
		if caller.Status == state.AppDeleted || target.Status == state.AppDeleted || caller.AccountID == "" || caller.AccountID != target.AccountID ||
			caller.ProjectID == "" || caller.ProjectID != target.ProjectID || caller.PreviewOfSlug != "" || target.PreviewOfSlug != "" {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		return gateway.ServiceCaller{AppID: caller.ID, AccountID: caller.AccountID}, nil
	}
}

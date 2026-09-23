package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceProxyResolver gives project PR previews an environment-scoped
// service namespace. A preview first looks for the named workload inside its
// own account/project/PR tuple. When that sibling is absent, resolution falls
// back to the production slug; newServiceProxyAuthorizer then applies the
// project's preview_service_policy before any endpoint lookup or wake.
//
// Production callers, standalone previews, developer sessions, and legacy
// preview rows without project identity retain global slug resolution.
func newServiceProxyResolver(store state.Store) gateway.ServiceProxyResolver {
	return func(ctx context.Context, callerAppID, service string) (gateway.ServiceTarget, bool, error) {
		scopedPreviewCaller := false
		if isAppID(callerAppID) {
			caller, err := store.AppByID(ctx, callerAppID)
			switch {
			case err == nil:
				if caller.PreviewOfSlug != "" && caller.PreviewPrNumber > 0 && caller.ProjectID != "" {
					scopedPreviewCaller = true
					preview, lookupErr := store.PreviewAppByProjectWorkload(
						ctx, caller.AccountID, caller.ProjectID, caller.PreviewPrNumber, service,
					)
					if lookupErr == nil {
						return serviceTargetFromApp(preview, true), preview.ID != "", nil
					}
					if !errors.Is(lookupErr, state.ErrNotFound) {
						return gateway.ServiceTarget{}, false, fmt.Errorf("resolve preview service %q: %w", service, lookupErr)
					}
				}
			case errors.Is(err, state.ErrNotFound):
				// Preserve the existing response contract: resolve the named
				// service, then let the authorizer turn an unknown caller into a
				// tenant-boundary denial rather than a registry outage.
			default:
				return gateway.ServiceTarget{}, false, fmt.Errorf("resolve caller app: %w", err)
			}
		}

		app, err := store.AppBySlug(ctx, service)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceTarget{}, false, nil
		}
		if err != nil {
			return gateway.ServiceTarget{}, false, fmt.Errorf("resolve service %q: %w", service, err)
		}
		if scopedPreviewCaller && app.PreviewOfSlug != "" {
			// A generated preview slug is not an escape hatch from the
			// environment key. Only the scoped workload lookup above may
			// select a preview target for a project PR caller.
			return gateway.ServiceTarget{}, false, nil
		}
		return serviceTargetFromApp(app, false), app.ID != "", nil
	}
}

func serviceTargetFromApp(app state.App, isPreview bool) gateway.ServiceTarget {
	return gateway.ServiceTarget{
		AppID:            app.ID,
		PreviewScoped:    isPreview,
		AppProtocol:      app.AppProtocol,
		WebSocketEnabled: app.WebSocketEnabled,
	}
}

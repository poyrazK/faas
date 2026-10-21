package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newWakeAheadDeclared lists a caller's declared service dependencies as
// target app IDs for wake-ahead (ADR-1137), in declaration order. Each name is
// resolved and authorized exactly as a call from the caller would be, so
// wake-ahead never restores a service the caller could not reach. Preview and
// scenario-test callers declare nothing: like the learner, wake-ahead does not
// act on their behalf. A name that cannot be resolved or authorized is
// skipped rather than failing the others.
func newWakeAheadDeclared(store state.Store, resolve gateway.ServiceProxyResolver, authorize gateway.ServiceProxyAuthorizer) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, appID string) ([]string, error) {
		if !isAppID(appID) {
			return nil, nil
		}
		caller, err := store.AppByID(ctx, appID)
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if caller.Status == state.AppDeleted || caller.PreviewOfSlug != "" {
			return nil, nil
		}
		var targets []string
		for _, binding := range caller.Manifest.ServiceBindings {
			service := strings.TrimSpace(binding.Service)
			if service == "" {
				continue
			}
			target, ok, err := resolve(ctx, appID, service)
			if err != nil || !ok || target.AppID == "" || target.AppID == appID || target.PreviewScoped ||
				target.ScenarioTestRunID != "" || slices.Contains(targets, target.AppID) {
				continue
			}
			if _, err := authorize(ctx, appID, target.AppID); err != nil {
				continue
			}
			if targets = append(targets, target.AppID); len(targets) == api.ServiceWakeAheadMaxTargets {
				break
			}
		}
		return targets, nil
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// serviceWakeAheadEnv is the operator kill switch for ADR-950. The drop-in
// sets it on; customers still opt in per caller with
// x-gregale-service-wake-ahead, so the switch only exists to stop all
// speculative restores on a node without a deploy.
const serviceWakeAheadEnv = "FAAS_GATEWAY_SERVICE_WAKE_AHEAD"

// newServiceWakeAheadPlanner builds the ADR-950 plan for a waking caller: its
// declared service bindings, each resolved and authorized through the same
// seams the node-local service proxy applies to a real call. A binding the
// caller could not actually call is never restored.
//
// Extracted from run() so opt-in, cap, and denial handling are testable
// without the daemon.
func newServiceWakeAheadPlanner(store state.Store, resolve gateway.ServiceProxyResolver, authorize gateway.ServiceProxyAuthorizer) gateway.ServiceWakeAheadPlanner {
	return func(ctx context.Context, callerAppID string) (gateway.ServiceWakeAheadPlan, error) {
		var plan gateway.ServiceWakeAheadPlan
		if !isAppID(callerAppID) {
			return plan, nil
		}
		caller, err := store.AppByID(ctx, callerAppID)
		if errors.Is(err, state.ErrNotFound) {
			return plan, nil
		}
		if err != nil {
			return plan, fmt.Errorf("service wake-ahead: load caller %q: %w", callerAppID, err)
		}
		if caller.Status == state.AppDeleted || caller.Manifest.EffectiveServiceWakeAhead() != api.ServiceWakeAheadDeclared {
			return plan, nil
		}
		seen := make(map[string]struct{}, len(caller.Manifest.ServiceBindings))
		for _, binding := range caller.Manifest.ServiceBindings {
			name := strings.TrimSpace(binding.Service)
			if name == "" {
				continue
			}
			if len(plan.Targets) >= api.ServiceWakeAheadMaxTargets {
				plan.Truncated++
				continue
			}
			target, ok, err := resolve(ctx, callerAppID, name)
			if errors.Is(err, gateway.ErrServiceProxyNotFound) {
				ok, err = false, nil
			}
			if err != nil {
				return plan, fmt.Errorf("service wake-ahead: resolve %q: %w", name, err)
			}
			if !ok || target.AppID == "" {
				plan.Unresolved++
				continue
			}
			if _, dup := seen[target.AppID]; dup {
				continue
			}
			seen[target.AppID] = struct{}{}
			if _, err := authorize(ctx, callerAppID, target.AppID); err != nil {
				if isServiceProxyDenial(err) {
					plan.Denied++
					continue
				}
				return plan, fmt.Errorf("service wake-ahead: authorize %q: %w", name, err)
			}
			app, err := store.AppByID(ctx, target.AppID)
			if errors.Is(err, state.ErrNotFound) {
				plan.Unresolved++
				continue
			}
			if err != nil {
				return plan, fmt.Errorf("service wake-ahead: load target %q: %w", name, err)
			}
			resolved, ok, err := (pgRouter{store: store}).toApp(ctx, app)
			if err != nil {
				return plan, fmt.Errorf("service wake-ahead: project target %q: %w", name, err)
			}
			if !ok {
				plan.Unresolved++
				continue
			}
			plan.Targets = append(plan.Targets, resolved)
		}
		return plan, nil
	}
}

// isServiceProxyDenial reports a customer-policy refusal, as opposed to a
// platform failure, from the service proxy authorizer.
func isServiceProxyDenial(err error) bool {
	for _, denial := range []error{
		gateway.ErrServiceProxyDenied,
		gateway.ErrServiceProxyBindingDenied,
		gateway.ErrServiceProxyCallerDenied,
		gateway.ErrServiceProxyPreviewDenied,
		gateway.ErrServiceProxyPreviewProductionDenied,
	} {
		if errors.Is(err, denial) {
			return true
		}
	}
	return false
}

package servicebinding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// NewAliasAllowed is shared by the guest DNS and HTTP proxy paths.
// DNS controls discoverability; this HTTP check remains authoritative when a
// guest bypasses DNS and sends a .internal Host header to the bridge directly.
func NewAliasAllowed(store interface {
	AppByID(context.Context, string) (state.App, error)
}) gateway.ServiceAliasAllowed {
	return func(ctx context.Context, callerAppID, service string) (bool, error) {
		if !isAppID(callerAppID) || store == nil {
			return false, nil
		}
		caller, err := store.AppByID(ctx, callerAppID)
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("load service alias caller: %w", err)
		}
		if caller.AccountID == "" || caller.Status == state.AppDeleted {
			return false, nil
		}
		for _, binding := range caller.Manifest.ServiceBindings {
			if strings.EqualFold(strings.TrimSpace(binding.Service), service) {
				return true, nil
			}
		}
		if allowed, err := projectAliasAllowed(ctx, store, caller, service); err != nil || allowed {
			return allowed, err
		}
		if qualificationAliases, ok := store.(state.EnvironmentQualificationServiceAliasStore); ok {
			allowed, err := qualificationAliases.EnvironmentQualificationServiceAliasAllowed(ctx, caller.ID, service)
			if err != nil {
				return false, fmt.Errorf("check scoped qualification service alias: %w", err)
			}
			if allowed {
				return true, nil
			}
		}
		return false, nil
	}
}

// projectAliasAllowed answers the alias for any service of the caller's own
// project under the project policy (ADR-20261022160248), which the proxy
// already admits without a declaration. Scenario-test members keep the binding
// inventory. A PR preview names its project's production members; the
// resolver then routes to its own PR's sibling or the permitted fallback.
func projectAliasAllowed(ctx context.Context, store any, caller state.App, service string) (bool, error) {
	if caller.Manifest.EffectiveServiceBindingPolicy() != api.ServiceBindingPolicyProject || caller.ProjectID == "" ||
		(caller.PreviewOfSlug != "" && caller.PreviewPrNumber == 0) {
		return false, nil
	}
	reader, ok := store.(state.ProjectServiceReader)
	if !ok {
		return false, nil
	}
	production := caller
	production.PreviewOfSlug = ""
	target, err := state.AppByServiceName(ctx, reader, production, service)
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve project service alias: %w", err)
	}
	return target.AccountID == caller.AccountID && target.ProjectID == caller.ProjectID &&
		target.Status != state.AppDeleted && target.PreviewOfSlug == "", nil
}

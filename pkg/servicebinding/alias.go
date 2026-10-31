package servicebinding

import (
	"context"
	"errors"
	"fmt"

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
			if api.SameServiceName(binding.Service, service) {
				return true, nil
			}
		}
		if allowed, err := projectAliasAllowed(ctx, store, caller, service); err != nil || allowed {
			return allowed, err
		}
		if allowed, err := namespaceAliasAllowed(ctx, store, caller, service); err != nil || allowed {
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

// namespaceAliasAllowed answers the alias for a sibling in the caller's
// registered developer or test namespace (`gregale dev --all`, scenario
// runs; ADR-20261025121530), which the proxy already resolves within that
// namespace and never to production.
func namespaceAliasAllowed(ctx context.Context, store any, caller state.App, service string) (bool, error) {
	if caller.PreviewOfSlug == "" || caller.PreviewPrNumber != 0 {
		return false, nil
	}
	namespaces, ok := store.(interface {
		ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error)
		ScenarioTestAppByWorkload(context.Context, string, string, string) (state.App, error)
	})
	if !ok {
		return false, nil
	}
	member, err := namespaces.ScenarioTestMemberByApp(ctx, caller.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load caller namespace: %w", err)
	}
	target, err := namespaces.ScenarioTestAppByWorkload(ctx, member.AccountID, member.RunID, service)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve namespace service alias: %w", err)
	}
	return target.ID != "" && target.AccountID == caller.AccountID, nil
}

// projectAliasAllowed answers the alias for any service of the caller's own
// project under the project policy (ADR-20261022160248), which the proxy
// already admits without a declaration. Members of a developer or test
// namespace use namespaceAliasAllowed instead. A PR preview names its
// project's production members; the
// resolver then routes to its own PR's sibling or the permitted fallback.
func projectAliasAllowed(ctx context.Context, store any, caller state.App, service string) (bool, error) {
	if caller.Manifest.EffectiveServiceBindingPolicy() != api.ServiceBindingPolicyProject || caller.ProjectID == "" ||
		(caller.PreviewOfSlug != "" && caller.PreviewPrNumber == 0) {
		return false, nil
	}
	production := caller
	production.PreviewOfSlug = ""
	var target state.App
	var err error
	switch reader := store.(type) {
	case state.ProjectServiceLookupReader:
		// The gateway's pinned service-policy view (ADR-570) has only this
		// indexed read. Requiring the inventory reader refused every
		// undeclared same-project alias call there (Hunt 14 H14-81).
		target, err = state.AppByServiceLookup(ctx, reader, production, service)
	case state.ProjectServiceReader:
		target, err = state.AppByServiceName(ctx, reader, production, service)
	default:
		return false, nil
	}
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve project service alias: %w", err)
	}
	return target.AccountID == caller.AccountID && target.ProjectID == caller.ProjectID &&
		target.Status != state.AppDeleted && target.PreviewOfSlug == "", nil
}

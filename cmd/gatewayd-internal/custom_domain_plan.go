package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func customDomainPlanAllowed(domain state.CustomDomain, plan api.Plan) bool {
	return !state.IsWildcardCustomDomain(domain.Domain) || plan.WildcardDomainsAllowed()
}

// A cached route must follow the current billing owner's plan even when a
// plan-change invalidation has not reached this gateway yet.
func (r pgRouter) cachedCustomDomainPlanAllowed(ctx context.Context, domain state.CustomDomain) (bool, error) {
	if !state.IsWildcardCustomDomain(domain.Domain) {
		return true, nil
	}
	app, err := r.store.AppByID(ctx, domain.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owner, err := state.AppResourceAccount(ctx, r.store, app)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return customDomainPlanAllowed(domain, owner.Plan), nil
}

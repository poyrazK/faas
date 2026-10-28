package state

import "context"

// RouteGuardRelease says whether the route guards on a canary belong to
// capabilities released for its account (ADR-20261028110044). An operator can
// switch a capability off platform-wide or release a preview to some accounts
// (ADR-20261023115741); a guard whose capability is off must neither hold a
// rollout nor record evidence the account cannot read.
type RouteGuardRelease struct {
	// Health is the critical route health guard (route-health-canary-guard).
	Health bool
	// Requirements is the canary route gate (route-requirements).
	Requirements bool
}

type routeGuardReleaseKey struct{}

// WithRouteGuardRelease carries apid's decision to the store's canary
// transitions.
func WithRouteGuardRelease(ctx context.Context, release RouteGuardRelease) context.Context {
	return context.WithValue(ctx, routeGuardReleaseKey{}, release)
}

// routeGuardRelease is the decision on ctx. A call without one keeps both
// guards, as before capability release reached the store.
func routeGuardRelease(ctx context.Context) RouteGuardRelease {
	if release, ok := ctx.Value(routeGuardReleaseKey{}).(RouteGuardRelease); ok {
		return release
	}
	return RouteGuardRelease{Health: true, Requirements: true}
}

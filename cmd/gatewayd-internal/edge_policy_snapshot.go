package main

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type pinnedPolicyGenerationKey struct{}

type pinnedPolicyGeneration struct {
	owner      *gatewaydEdgeRules
	generation uint64
}

func (g *gatewaydEdgeRules) PinHostPolicy(ctx context.Context, host string) (context.Context, error) {
	if g == nil || g.cache == nil || g.store == nil {
		return nil, errors.New("traffic policy source unavailable")
	}
	if g.RequiresOwnerPolicySnapshot() {
		return g.pinPublicRouteGraph(ctx, host)
	}
	// Owner-scoped convergence fences are checked by the handler once the
	// host's owner is resolved; the pin only refuses a generation change.
	prior, pinned := ctx.Value(pinnedPolicyGenerationKey{}).(pinnedPolicyGeneration)
	for attempt := 1; ; attempt++ {
		generation := g.cache.Generation()
		if pinned && (prior.owner != g || prior.generation != generation) {
			return nil, errors.New("traffic policy changed during snapshot resolution")
		}
		// ADR-1191: once the request's app is known the host's set includes that
		// app's app-wide rules, so the pin is per (host, app) like the cache.
		cached, err := g.loadHost(ctx, host)
		if err != nil {
			return nil, err
		}
		// The cached entry is shared with concurrent requests and must not be
		// mutated. Production loadHostUncached seals before publication, so this
		// copy shares that immutable seal; an unsealed fixture entry is sealed
		// privately here.
		entry := new(gateway.HostEntry)
		*entry = *cached
		if err := entry.SealPolicy(); err != nil {
			return nil, err
		}
		if generation == g.cache.Generation() {
			ctx = context.WithValue(ctx, pinnedPolicyGenerationKey{}, pinnedPolicyGeneration{g, generation})
			return gateway.WithPinnedHostPolicy(ctx, gateway.EdgeRuleCacheKey(host, gateway.EdgeRuleApp(ctx)), entry)
		}
		// Every invalidation, for any host, advances the shared generation.
		// With nothing pinned yet the read starts again; a request that
		// already pinned another host keeps one generation and fails closed.
		if pinned || attempt >= hostPolicyPinAttempts || ctx.Err() != nil {
			return nil, errors.New("traffic policy changed during snapshot resolution")
		}
	}
}

// hostPolicyPinAttempts bounds how often a first host pin re-reads after a
// concurrent invalidation before the request fails closed.
const hostPolicyPinAttempts = 3

// cachedHostRules reads the request's pinned policy for (host, app). A pinned
// host never falls back to the live cache: until the app's own pin exists
// (route substitution runs before admission) its host pin answers, and the
// app's app-wide rules (ADR-1191) join with the (host, app) pin. The
// authoritative host graph already carries the claiming app's app-wide routes.
func cachedHostRules[T any](g *gatewaydEdgeRules, ctx context.Context, host string, selectRules func(*gateway.HostEntry) []T) ([]T, bool) {
	key := gateway.EdgeRuleCacheKey(host, gateway.EdgeRuleApp(ctx))
	entry, ok := gateway.PinnedHostPolicy(ctx, key)
	if !ok && key != host {
		entry, ok = gateway.PinnedHostPolicy(ctx, host)
	}
	if !ok {
		entry, ok = g.cache.GetHost(key)
	}
	if !ok {
		return nil, false
	}
	return slices.Clone(selectRules(entry)), true
}

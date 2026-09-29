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
	generation := g.cache.Generation()
	prior, pinned := ctx.Value(pinnedPolicyGenerationKey{}).(pinnedPolicyGeneration)
	if g.Converging(host) || (pinned && (prior.owner != g || prior.generation != generation)) {
		return nil, errors.New("traffic policy changed during snapshot resolution")
	}
	entry, err := g.loadHost(ctx, host)
	if err != nil {
		return nil, err
	}
	// Direct cache fixtures and older cache writers may lack a seal. Production
	// loadHostUncached seals before publication; this seals a private value-copy.
	if err := entry.SealPolicy(); err != nil {
		return nil, err
	}
	if generation != g.cache.Generation() || g.Converging(host) {
		return nil, errors.New("traffic policy changed during snapshot resolution")
	}
	ctx = context.WithValue(ctx, pinnedPolicyGenerationKey{}, pinnedPolicyGeneration{g, generation})
	return gateway.WithPinnedHostPolicy(ctx, host, entry)
}

func cachedHostRules[T any](g *gatewaydEdgeRules, ctx context.Context, host string, selectRules func(*gateway.HostEntry) []T) ([]T, bool) {
	entry, ok := gateway.PinnedHostPolicy(ctx, host)
	if !ok {
		entry, ok = g.cache.GetHost(host)
	}
	if !ok {
		return nil, false
	}
	return slices.Clone(selectRules(entry)), true
}

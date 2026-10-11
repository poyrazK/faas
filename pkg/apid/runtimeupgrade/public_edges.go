package runtimeupgrade

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// PublicEdgeControls is private platform administration, without a route or CLI.
type PublicEdgeControls struct {
	Store state.RuntimeUpgradePublicEdgeRosterStore
}

func (c PublicEdgeControls) Review(ctx context.Context, expected, gateway, topology string, members []state.RuntimeUpgradePublicEdgeMember) (state.RuntimeUpgradePublicEdgeRoster, error) {
	if c.Store == nil {
		return state.RuntimeUpgradePublicEdgeRoster{}, state.ErrInvalidArgument
	}
	return c.Store.ReviewRuntimeUpgradePublicEdgeRoster(ctx, expected, gateway, topology, members)
}

func (c PublicEdgeControls) Status(ctx context.Context) (state.RuntimeUpgradePublicEdgeRoster, error) {
	if c.Store == nil {
		return state.RuntimeUpgradePublicEdgeRoster{}, state.ErrInvalidArgument
	}
	return c.Store.RuntimeUpgradePublicEdgeRoster(ctx)
}

func (c PublicEdgeControls) Observe(ctx context.Context, expected string) (state.RuntimeUpgradePublicEdgeObservation, error) {
	if c.Store == nil {
		return state.RuntimeUpgradePublicEdgeObservation{}, state.ErrInvalidArgument
	}
	return c.Store.ObserveRuntimeUpgradePublicEdges(ctx, expected)
}

func (c PublicEdgeControls) ObserveActivity(ctx context.Context, expected string) (state.RuntimeUpgradePublicEdgeActivityObservation, error) {
	store, ok := c.Store.(state.RuntimeUpgradePublicEdgeActivityVerifier)
	if !ok {
		return state.RuntimeUpgradePublicEdgeActivityObservation{}, state.ErrInvalidArgument
	}
	return store.ObserveRuntimeUpgradePublicEdgeActivity(ctx, expected)
}

func (c PublicEdgeControls) ObserveCoverage(ctx context.Context, expected string) (state.RuntimeUpgradePublicEdgeCoverageObservation, error) {
	store, ok := c.Store.(state.RuntimeUpgradePublicEdgeCoverageVerifier)
	if !ok {
		return state.RuntimeUpgradePublicEdgeCoverageObservation{}, state.ErrInvalidArgument
	}
	return store.ObserveRuntimeUpgradePublicEdgeCoverage(ctx, expected)
}

package runtimeupgrade

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// GatewayRosterControls is a trusted platform-administrative seam. No customer
// route, CLI command, automatic inventory or public Apply is registered.
type GatewayRosterControls struct {
	Store state.RuntimeUpgradeGatewayRosterStore
}

func (c GatewayRosterControls) Review(ctx context.Context, expected string, members []state.RuntimeUpgradeGatewayMember) (state.RuntimeUpgradeGatewayRoster, error) {
	if c.Store == nil {
		return state.RuntimeUpgradeGatewayRoster{}, state.ErrInvalidArgument
	}
	r, err := c.Store.ReviewRuntimeUpgradeGatewayRoster(ctx, expected, members)
	if err != nil {
		return state.RuntimeUpgradeGatewayRoster{}, fmt.Errorf("review runtime gateway roster: %w", err)
	}
	return r, nil
}

func (c GatewayRosterControls) Status(ctx context.Context) (state.RuntimeUpgradeGatewayRoster, error) {
	if c.Store == nil {
		return state.RuntimeUpgradeGatewayRoster{}, state.ErrInvalidArgument
	}
	r, err := c.Store.RuntimeUpgradeGatewayRoster(ctx)
	if err != nil {
		return state.RuntimeUpgradeGatewayRoster{}, fmt.Errorf("read runtime gateway roster: %w", err)
	}
	return r, nil
}

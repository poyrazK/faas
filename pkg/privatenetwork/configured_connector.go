package privatenetwork

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// ConfiguredNetwork describes a provider attachment that an operator has
// already created. It is useful for a first deployment and for local/CI
// environments; a cloud adapter can implement Connector without changing the
// reconciler or API contract.
type ConfiguredNetwork struct {
	ID     string
	Region string
	Ready  bool
	Detail string
}

// ConfiguredConnector is a deterministic connector backed by operator state.
// It deliberately never claims readiness for an unknown network or a region
// mismatch, which keeps the default posture fail-closed.
type ConfiguredConnector struct {
	networks map[string]ConfiguredNetwork
}

func NewConfiguredConnector(networks []ConfiguredNetwork) (*ConfiguredConnector, error) {
	c := &ConfiguredConnector{networks: make(map[string]ConfiguredNetwork, len(networks))}
	for _, network := range networks {
		id := strings.TrimSpace(network.ID)
		region := strings.TrimSpace(network.Region)
		if id == "" || region == "" {
			return nil, fmt.Errorf("privatenetwork: configured network id and region are required")
		}
		if _, exists := c.networks[id]; exists {
			return nil, fmt.Errorf("privatenetwork: duplicate configured network %q", id)
		}
		network.ID, network.Region = id, region
		c.networks[id] = network
	}
	return c, nil
}

func (c *ConfiguredConnector) Check(_ context.Context, attachment state.AppPrivateNetworkAttachment) (CheckResult, error) {
	network, ok := c.networks[attachment.NetworkID]
	if !ok {
		return CheckResult{Detail: "provider network is not configured on this node"}, nil
	}
	if network.Region != attachment.Region {
		return CheckResult{}, fmt.Errorf("provider network %q belongs to region %q, not %q", attachment.NetworkID, network.Region, attachment.Region)
	}
	if !network.Ready {
		return CheckResult{Detail: network.Detail}, nil
	}
	return CheckResult{Ready: true, Detail: network.Detail}, nil
}

// FuncRouteApplier adapts a function to RouteApplier, keeping tests and
// daemon wiring small without forcing a concrete netlink dependency here.
type FuncRouteApplier func(ctx context.Context, appID string, cidrs []netip.Prefix) error

func (f FuncRouteApplier) Apply(ctx context.Context, appID string, cidrs []netip.Prefix) error {
	return f(ctx, appID, cidrs)
}

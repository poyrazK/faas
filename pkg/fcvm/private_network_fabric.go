package fcvm

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

// ReconcilePrivateNetworkFabric ensures the dedicated host bridge for one
// Gregale-owned network exists on this vmmd node. The operation is additive
// and idempotent: a capture-capable runner probes an existing bridge after a
// process restart, while the in-memory cache avoids duplicate link-add calls
// during steady state. Workload attachment and cross-node transport are
// deliberately separate layers on top of this stable bridge identity.
func (m *Manager) ReconcilePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	spec, err := privatenetwork.NewFabricSpecFromValues(accountID, networkID, region, cidr.String())
	if err != nil {
		return err
	}
	plan, err := privatenetwork.BuildFabricPlan(spec)
	if err != nil {
		return err
	}
	key := spec.AccountID + "\x00" + spec.NetworkID

	m.privateNetworkFabricMu.Lock()
	defer m.privateNetworkFabricMu.Unlock()

	known := false
	if prior, ok := m.privateNetworkFabric[key]; ok {
		known = prior == spec.CIDR
	}
	if !known && m.captureRunner != nil {
		_, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", plan.BridgeName})
		known = probeErr == nil
	}
	if !known {
		if err := m.run.Run(ctx, plan.Setup[0]); err != nil {
			return fmt.Errorf("fcvm: create private network bridge %s: %w", plan.BridgeName, err)
		}
	}
	// The network CIDR is immutable in state, but replace the prior gateway
	// defensively if a caller attempts to reconcile a changed spec.
	if prior, ok := m.privateNetworkFabric[key]; ok && prior != spec.CIDR {
		oldSpec, oldErr := privatenetwork.NewFabricSpecFromValues(spec.AccountID, spec.NetworkID, spec.Region, prior.String())
		if oldErr == nil {
			if oldPlan, buildErr := privatenetwork.BuildFabricPlan(oldSpec); buildErr == nil {
				_ = m.run.Run(ctx, []string{"ip", "addr", "del", oldPlan.GatewayCIDR.String(), "dev", oldPlan.BridgeName})
			}
		}
	}
	for _, argv := range plan.Setup[1:] {
		if err := m.run.Run(ctx, argv); err != nil {
			return fmt.Errorf("fcvm: reconcile private network bridge %s: %w", plan.BridgeName, err)
		}
	}
	m.privateNetworkFabric[key] = spec.CIDR
	return nil
}

// RemovePrivateNetworkFabric removes a node-local network bridge. It is used
// by the detach/delete path and is safe to replay after the bridge is gone.
func (m *Manager) RemovePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	spec, err := privatenetwork.NewFabricSpecFromValues(accountID, networkID, region, cidr.String())
	if err != nil {
		return err
	}
	plan, err := privatenetwork.BuildFabricPlan(spec)
	if err != nil {
		return err
	}
	key := spec.AccountID + "\x00" + spec.NetworkID

	m.privateNetworkFabricMu.Lock()
	defer m.privateNetworkFabricMu.Unlock()
	if err := m.run.Run(ctx, plan.Teardown[0]); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if m.captureRunner != nil {
			if _, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", plan.BridgeName}); probeErr != nil {
				delete(m.privateNetworkFabric, key)
				return nil
			}
		}
		return fmt.Errorf("fcvm: remove private network bridge %s: %w", plan.BridgeName, err)
	}
	delete(m.privateNetworkFabric, key)
	return nil
}

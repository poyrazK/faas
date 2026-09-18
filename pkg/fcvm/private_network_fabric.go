package fcvm

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

// PrivateNetworkTransportConfig enables the optional node-to-node transport
// for Gregale-owned networks. The underlay is operator-managed (for example
// Tailscale or WireGuard); vmmd only programs a deterministic VXLAN link over
// it. Keep Enabled false until every node in a region has compatible overlay
// reachability and mTLS/control-plane policy.
type PrivateNetworkTransportConfig struct {
	Enabled          bool
	OverlayInterface string
	LocalAddress     netip.Addr
	PeerAddresses    []netip.Addr
}

// WithPrivateNetworkTransport configures the optional transport before vmmd
// starts serving RPCs. The slice is copied so callers cannot mutate topology
// while a reconciliation is in flight.
func (m *Manager) WithPrivateNetworkTransport(cfg PrivateNetworkTransportConfig) *Manager {
	cfg.OverlayInterface = strings.TrimSpace(cfg.OverlayInterface)
	cfg.PeerAddresses = append([]netip.Addr(nil), cfg.PeerAddresses...)
	m.privateNetworkTransportCfg = cfg
	return m
}

// ReconcilePrivateNetworkFabric ensures the dedicated host bridge for one
// Gregale-owned network exists on this vmmd node. The operation is additive
// and idempotent: a capture-capable runner probes an existing bridge after a
// process restart, while the in-memory cache avoids duplicate link-add calls
// during steady state. When configured, the same reconciliation also attaches
// a deterministic VXLAN link to the bridge for regional cross-node traffic.
func (m *Manager) ReconcilePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	return m.reconcilePrivateNetworkFabric(ctx, accountID, networkID, region, cidr, nil, false)
}

// ReconcilePrivateNetworkFabricWithPeers applies an authoritative regional
// peer roster to the transport link as part of fabric reconciliation. The
// roster is supplied by schedd from active compute-node registrations, so a
// node join or drain converges without editing every vmmd config file.
func (m *Manager) ReconcilePrivateNetworkFabricWithPeers(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix, peers []netip.Addr) error {
	return m.reconcilePrivateNetworkFabric(ctx, accountID, networkID, region, cidr, peers, true)
}

func (m *Manager) reconcilePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix, managedPeers []netip.Addr, peersManaged bool) error {
	spec, err := privatenetwork.NewFabricSpecFromValues(accountID, networkID, region, cidr.String())
	if err != nil {
		return err
	}
	plan, err := privatenetwork.BuildFabricPlan(spec)
	if err != nil {
		return err
	}
	transportCfg := m.privateNetworkTransportCfg
	var transportPlan privatenetwork.FabricTransportPlan
	if transportCfg.Enabled {
		peerAddresses := transportCfg.PeerAddresses
		if peersManaged {
			peerAddresses = managedPeers
		}
		transportPlan, err = privatenetwork.BuildFabricTransportPlan(privatenetwork.FabricTransportSpec{
			Fabric:           spec,
			OverlayInterface: transportCfg.OverlayInterface,
			LocalAddress:     transportCfg.LocalAddress,
			PeerAddresses:    peerAddresses,
		})
		if err != nil {
			return err
		}
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
	if transportCfg.Enabled {
		transportKnown := m.privateNetworkTransport[key]
		peerSetKey := transportPeerSetKey(transportPlan.Spec.PeerAddresses)
		priorPeerSetKey, peerSetKnown := m.privateNetworkTransportPeers[key]
		peerSetKnown = peerSetKnown && priorPeerSetKey == peerSetKey
		transportCreated := false
		if !transportKnown && m.captureRunner != nil {
			_, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", transportPlan.LinkName})
			transportKnown = probeErr == nil
		}
		if !transportKnown {
			for _, argv := range transportPlan.Setup {
				if err := m.run.Run(ctx, argv); err != nil {
					return fmt.Errorf("fcvm: reconcile private network transport %s: %w", transportPlan.LinkName, err)
				}
			}
			transportCreated = true
		}
		if !transportCreated && !peerSetKnown {
			for _, argv := range transportPlan.PeerSync {
				if err := m.run.Run(ctx, argv); err != nil {
					return fmt.Errorf("fcvm: reconcile private network transport peers %s: %w", transportPlan.LinkName, err)
				}
			}
		}
		// A newly created link already received the canonical FDB entries as
		// part of Setup. Existing links need the flush-and-replace sync above.
		m.privateNetworkTransportPeers[key] = peerSetKey
		m.privateNetworkTransport[key] = true
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
	if m.privateNetworkTransportCfg.Enabled {
		transportPlan, transportErr := privatenetwork.BuildFabricTransportPlan(privatenetwork.FabricTransportSpec{
			Fabric:           spec,
			OverlayInterface: m.privateNetworkTransportCfg.OverlayInterface,
			LocalAddress:     m.privateNetworkTransportCfg.LocalAddress,
			PeerAddresses:    m.privateNetworkTransportCfg.PeerAddresses,
		})
		if transportErr != nil {
			return transportErr
		}
		transportKnown := m.privateNetworkTransport[key]
		if !transportKnown && m.captureRunner != nil {
			_, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", transportPlan.LinkName})
			transportKnown = probeErr == nil
		}
		if transportKnown {
			if err := m.run.Run(ctx, transportPlan.Teardown[0]); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if m.captureRunner != nil {
					if _, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", transportPlan.LinkName}); probeErr != nil {
						m.privateNetworkTransport[key] = false
					} else {
						return fmt.Errorf("fcvm: remove private network transport %s: %w", transportPlan.LinkName, err)
					}
				} else {
					return fmt.Errorf("fcvm: remove private network transport %s: %w", transportPlan.LinkName, err)
				}
			} else {
				m.privateNetworkTransport[key] = false
			}
		}
	}
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
	delete(m.privateNetworkTransport, key)
	delete(m.privateNetworkTransportPeers, key)
	return nil
}

func transportPeerSetKey(peers []netip.Addr) string {
	parts := make([]string, 0, len(peers))
	for _, peer := range peers {
		parts = append(parts, peer.String())
	}
	return strings.Join(parts, ",")
}

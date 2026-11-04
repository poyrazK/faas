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
	MTU              int
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

// WithPrivateNetworkTransportIdentitySource installs the regional durable VNI allocator.
func (m *Manager) WithPrivateNetworkTransportIdentitySource(source func(context.Context, string, string, string) (uint32, error)) *Manager {
	m.privateNetworkVNISource = source
	return m
}

func (m *Manager) privateNetworkVNI(ctx context.Context, spec privatenetwork.FabricSpec) (uint32, error) {
	if m.privateNetworkVNISource == nil {
		return 0, nil
	}
	return m.privateNetworkVNISource(ctx, spec.AccountID, spec.NetworkID, spec.Region)
}

// ReconcilePrivateNetworkFabric ensures the dedicated host bridge for one
// Gregale-owned network exists on this vmmd node. The operation is additive
// and idempotent: a capture-capable runner probes an existing bridge after a
// process restart, while the in-memory cache avoids duplicate link-add calls
// during steady state. When configured, the same reconciliation also attaches
// a deterministic VXLAN link to the bridge for regional cross-node traffic.
func (m *Manager) ReconcilePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	if err := m.reconcilePrivateNetworkFabric(ctx, accountID, networkID, region, cidr, nil, false); err != nil {
		return err
	}
	return m.renderHostSMTPAllowlistRules(ctx, false)
}

// ReconcilePrivateNetworkFabricWithPeers applies an authoritative regional
// peer roster to the transport link as part of fabric reconciliation. The
// roster is supplied by schedd from active compute-node registrations, so a
// node join or drain converges without editing every vmmd config file.
func (m *Manager) ReconcilePrivateNetworkFabricWithPeers(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix, peers []netip.Addr) error {
	if err := m.reconcilePrivateNetworkFabric(ctx, accountID, networkID, region, cidr, peers, true); err != nil {
		return err
	}
	return m.renderHostSMTPAllowlistRules(ctx, false)
}

// CheckPrivateNetworkFabric inspects the host state after reconciliation. It
// deliberately uses read-only iproute2 commands so a scheduler retry can
// distinguish an acknowledged mutation from a dataplane that is still
// missing a link, bridge membership, or regional FDB peer.
func (m *Manager) CheckPrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix, managedPeers []netip.Addr, peersManaged bool) (privatenetwork.FabricReadiness, error) {
	m.privateNetworkFabricMu.Lock()
	defer m.privateNetworkFabricMu.Unlock()
	if m.captureRunner == nil {
		return privatenetwork.FabricReadiness{Detail: "fabric readiness probe unavailable"}, nil
	}
	spec, err := privatenetwork.NewFabricSpecFromValues(accountID, networkID, region, cidr.String())
	if err != nil {
		return privatenetwork.FabricReadiness{}, err
	}
	plan, err := privatenetwork.BuildFabricPlan(spec)
	if err != nil {
		return privatenetwork.FabricReadiness{}, err
	}
	readiness := privatenetwork.FabricReadiness{Supported: true}
	bridgeLinkOutput, bridgeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", plan.BridgeName})
	if bridgeErr != nil {
		readiness.Detail = fmt.Sprintf("bridge %s is unavailable: %v", plan.BridgeName, bridgeErr)
		return readiness, nil
	}
	if mtu := parsePrivateLinkMTU(bridgeLinkOutput); m.privateNetworkMTU() != 0 && mtu != 0 && mtu != m.privateNetworkMTU() {
		readiness.Detail = fmt.Sprintf("private bridge MTU drift: got %d want %d", mtu, m.privateNetworkMTU())
		return readiness, nil
	}
	namespace := privatenetwork.RouterNamespace(spec.AccountID, spec.Region)
	host := privatenetwork.RouterHostLink(spec.AccountID, spec.NetworkID)
	peer := privatenetwork.RouterPeerLink(spec.AccountID, spec.NetworkID)
	membership, memberErr := m.captureRunner.RunCapture(ctx, []string{"bridge", "link", "show", "dev", host})
	if memberErr != nil || !strings.Contains(string(membership), "master "+plan.BridgeName) {
		readiness.Detail = fmt.Sprintf("private router endpoint is not attached to its network bridge: %v", memberErr)
		return readiness, nil
	}
	addresses, addrErr := m.captureRunner.RunCapture(ctx, []string{"ip", "-n", namespace, "-4", "-o", "addr", "show", "dev", peer})
	if addrErr != nil || !strings.Contains(string(addresses), "inet "+plan.GatewayCIDR.String()+" ") {
		readiness.Detail = fmt.Sprintf("private router gateway address is unavailable: %v", addrErr)
		return readiness, nil
	}
	transportCfg := m.privateNetworkTransportCfg
	if !transportCfg.Enabled {
		if peersManaged && len(managedPeers) > 0 {
			readiness.Detail = "regional transport is disabled"
			return readiness, nil
		}
		readiness.Ready = true
		readiness.Detail = "fabric bridge ready"
		return readiness, nil
	}
	peerAddresses := transportCfg.PeerAddresses
	if peersManaged {
		peerAddresses = managedPeers
	}
	vni, err := m.privateNetworkVNI(ctx, spec)
	if err != nil {
		return readiness, err
	}
	transportPlan, err := privatenetwork.BuildFabricTransportPlan(privatenetwork.FabricTransportSpec{
		Fabric:           spec,
		VNI:              vni,
		OverlayInterface: transportCfg.OverlayInterface,
		LocalAddress:     transportCfg.LocalAddress,
		PeerAddresses:    peerAddresses,
		MTU:              m.privateNetworkMTU(),
	})
	if err != nil {
		return privatenetwork.FabricReadiness{}, err
	}
	if err := m.checkPrivateTransportIdentity(ctx, transportPlan); err != nil {
		readiness.Detail = err.Error()
		return readiness, nil
	}
	readiness.ExpectedPeerAddresses = append([]netip.Addr(nil), transportPlan.Spec.PeerAddresses...)
	linkOutput, linkErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", transportPlan.LinkName})
	if linkErr != nil {
		readiness.Detail = fmt.Sprintf("transport link %s is unavailable: %v", transportPlan.LinkName, linkErr)
		return readiness, nil
	}
	if mtu := parsePrivateLinkMTU(linkOutput); mtu != 0 && mtu != m.privateNetworkMTU() {
		readiness.Detail = fmt.Sprintf("private transport MTU drift: got %d want %d", mtu, m.privateNetworkMTU())
		return readiness, nil
	}
	bridgeOutput, err := m.captureRunner.RunCapture(ctx, []string{"bridge", "link", "show", "dev", transportPlan.LinkName})
	if err != nil {
		readiness.Detail = fmt.Sprintf("transport link %s bridge membership unavailable: %v", transportPlan.LinkName, err)
		return readiness, nil
	}
	bridgeMarker := "master " + plan.BridgeName
	if !strings.Contains(string(bridgeOutput), bridgeMarker) {
		readiness.Detail = fmt.Sprintf("transport link %s is not attached to %s", transportPlan.LinkName, plan.BridgeName)
		return readiness, nil
	}
	fdbOutput, err := m.captureRunner.RunCapture(ctx, []string{"bridge", "fdb", "show", "dev", transportPlan.LinkName})
	if err != nil {
		readiness.Detail = fmt.Sprintf("transport link %s FDB unavailable: %v", transportPlan.LinkName, err)
		return readiness, nil
	}
	readiness.ObservedPeerAddresses = privatenetwork.ParseFabricTransportPeers(fdbOutput)
	missing, stale := privatenetwork.FabricPeerDrift(readiness.ExpectedPeerAddresses, readiness.ObservedPeerAddresses)
	if len(missing) != 0 || len(stale) != 0 {
		readiness.Detail = fmt.Sprintf("transport FDB drift (missing=%s stale=%s)", formatFabricPeers(missing), formatFabricPeers(stale))
		return readiness, nil
	}
	readiness.Ready = true
	readiness.Detail = "fabric transport ready"
	return readiness, nil
}

func formatFabricPeers(peers []netip.Addr) string {
	if len(peers) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(peers))
	for _, peer := range peers {
		parts = append(parts, peer.String())
	}
	return strings.Join(parts, ",")
}

func (m *Manager) reconcilePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix, managedPeers []netip.Addr, peersManaged bool) error {
	if err := m.validatePrivateTransportMTU(ctx); err != nil {
		return err
	}
	spec, err := privatenetwork.NewFabricSpecFromValues(accountID, networkID, region, cidr.String())
	if err != nil {
		return err
	}
	plan, err := privatenetwork.BuildFabricPlan(spec)
	if err != nil {
		return err
	}
	m.privateNetworkFabricMu.Lock()
	defer m.privateNetworkFabricMu.Unlock()
	transportCfg := m.privateNetworkTransportCfg
	var transportPlan privatenetwork.FabricTransportPlan
	if transportCfg.Enabled {
		peerAddresses := transportCfg.PeerAddresses
		if peersManaged {
			peerAddresses = managedPeers
		}
		vni, vniErr := m.privateNetworkVNI(ctx, spec)
		if vniErr != nil {
			return vniErr
		}
		transportPlan, err = privatenetwork.BuildFabricTransportPlan(privatenetwork.FabricTransportSpec{
			Fabric:           spec,
			VNI:              vni,
			OverlayInterface: transportCfg.OverlayInterface,
			LocalAddress:     transportCfg.LocalAddress,
			PeerAddresses:    peerAddresses,
			MTU:              m.privateNetworkMTU(),
		})
		if err != nil {
			return err
		}
	}
	key := spec.AccountID + "\x00" + spec.NetworkID

	known := false
	if prior, ok := m.privateNetworkFabric[key]; ok {
		known = prior == spec.CIDR
	}
	if m.captureRunner != nil {
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
	if mtu := m.privateNetworkMTU(); mtu != 0 {
		if err := m.run.Run(ctx, []string{"ip", "link", "set", "dev", plan.BridgeName, "mtu", fmt.Sprint(mtu)}); err != nil {
			return err
		}
	}
	if err := m.reconcilePrivateRouterLinkLocked(ctx, plan); err != nil {
		return err
	}
	if transportCfg.Enabled {
		transportKnown := m.privateNetworkTransport[key]
		peerSetKey := transportPeerSetKey(transportPlan.Spec.PeerAddresses)
		priorPeerSetKey, peerSetKnown := m.privateNetworkTransportPeers[key]
		peerSetKnown = peerSetKnown && priorPeerSetKey == peerSetKey
		transportCreated := false
		if m.captureRunner != nil {
			_, probeErr := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", transportPlan.LinkName})
			transportKnown = probeErr == nil
		}
		if transportKnown {
			drift := false
			if m.captureRunner != nil {
				drift = m.checkPrivateTransportIdentity(ctx, transportPlan) != nil
			} else if prior, ok := m.privateNetworkTransportIdentity[key]; ok {
				drift = prior.VNI != transportPlan.Spec.VNI || prior.LocalAddress != transportPlan.Spec.LocalAddress || prior.OverlayInterface != transportPlan.Spec.OverlayInterface
			}
			if drift {
				if err := m.run.Run(ctx, transportPlan.Teardown[0]); err != nil {
					return fmt.Errorf("remove drifted private transport: %w", err)
				}
				transportKnown = false
				m.privateNetworkTransport[key] = false
			}
		}
		if !transportKnown {
			for _, argv := range transportPlan.Setup {
				if err := m.run.Run(ctx, argv); err != nil {
					return fmt.Errorf("fcvm: reconcile private network transport %s: %w", transportPlan.LinkName, err)
				}
			}
			transportCreated = true
		}
		if !transportCreated && (!peerSetKnown || m.captureRunner != nil) {
			commands := transportPlan.PeerSync
			if m.captureRunner != nil {
				output, err := m.captureRunner.RunCapture(ctx, []string{"bridge", "fdb", "show", "dev", transportPlan.LinkName})
				if err != nil {
					return fmt.Errorf("inspect private transport peers: %w", err)
				}
				commands = privatenetwork.FabricTransportPeerCommands(transportPlan.LinkName, transportPlan.Spec.PeerAddresses, privatenetwork.ParseFabricTransportPeers(output))
			}
			for _, argv := range commands {
				if err := m.run.Run(ctx, argv); err != nil {
					return fmt.Errorf("fcvm: reconcile private network transport peers %s: %w", transportPlan.LinkName, err)
				}
			}
		}
		if !transportCreated {
			if err := m.run.Run(ctx, []string{"ip", "link", "set", "dev", transportPlan.LinkName, "mtu", fmt.Sprint(m.privateNetworkMTU())}); err != nil {
				return err
			}
			for _, argv := range transportPlan.Setup[1:3] {
				if err := m.run.Run(ctx, argv); err != nil {
					return fmt.Errorf("fcvm: repair private transport membership: %w", err)
				}
			}
		}
		// A newly created link already received the canonical FDB entries as
		// part of Setup. Existing links need the flush-and-replace sync above.
		m.privateNetworkTransportPeers[key] = peerSetKey
		m.privateNetworkTransport[key] = true
		if m.privateNetworkTransportIdentity == nil {
			m.privateNetworkTransportIdentity = make(map[string]privatenetwork.FabricTransportSpec)
		}
		m.privateNetworkTransportIdentity[key] = transportPlan.Spec
	} else if err := m.removePrivateTransportLocked(ctx, key, spec); err != nil {
		return err
	}
	m.privateNetworkFabric[key] = spec.CIDR
	return nil
}

// RemovePrivateNetworkFabric removes a node-local network bridge. It is used
// by the detach/delete path and is safe to replay after the bridge is gone.
func (m *Manager) RemovePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	if err := m.removePrivateNetworkFabric(ctx, accountID, networkID, region, cidr); err != nil {
		return err
	}
	return m.renderHostSMTPAllowlistRules(ctx, false)
}

func (m *Manager) removePrivateNetworkFabric(ctx context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
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
	if err := m.removePrivateTransportLocked(ctx, key, spec); err != nil {
		return err
	}

	if err := m.removePrivateRouterLinkLocked(ctx, plan); err != nil {
		return err
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

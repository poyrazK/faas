package fcvm

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

// privateNetworkFabricMu serializes router, bridge and peering mutations.
func (m *Manager) ensurePrivateRouterNamespaceLocked(ctx context.Context, accountID, region string) error {
	namespace := privatenetwork.RouterNamespace(accountID, region)
	key := accountID + "\x00" + region
	known := m.privateNetworkRouters[key]
	if m.captureRunner != nil {
		_, err := m.captureRunner.RunCapture(ctx, []string{"ip", "netns", "exec", namespace, "true"})
		known = err == nil
	}
	if !known {
		if err := m.run.Run(ctx, []string{"ip", "netns", "add", namespace}); err != nil {
			return fmt.Errorf("create private router: %w", err)
		}
	}
	commands, err := privatenetwork.RouterPolicyCommands(accountID, region, nil, nil)
	if err != nil {
		return err
	}
	// Ensure the default-drop base chains without flushing existing peerings.
	commands = append(commands[:1], commands[2:]...)
	if err := m.runAtomicNetworkPolicy(ctx, namespace, commands); err != nil {
		return fmt.Errorf("initialize private router firewall: %w", err)
	}
	if err := m.run.Run(ctx, []string{"ip", "netns", "exec", namespace, "sysctl", "-qw",
		"net.ipv4.ip_forward=1", "net.ipv4.conf.all.rp_filter=0", "net.ipv4.conf.default.rp_filter=0",
		"net.ipv4.conf.all.send_redirects=0", "net.ipv4.conf.default.send_redirects=0"}); err != nil {
		return err
	}
	if m.privateNetworkRouters == nil {
		m.privateNetworkRouters = make(map[string]bool)
	}
	m.privateNetworkRouters[key] = true
	return nil
}

func (m *Manager) reconcilePrivateRouterLinkLocked(ctx context.Context, plan privatenetwork.FabricPlan) error {
	spec := plan.Spec
	if err := m.ensurePrivateRouterNamespaceLocked(ctx, spec.AccountID, spec.Region); err != nil {
		return err
	}
	namespace := privatenetwork.RouterNamespace(spec.AccountID, spec.Region)
	host := privatenetwork.RouterHostLink(spec.AccountID, spec.NetworkID)
	peer := privatenetwork.RouterPeerLink(spec.AccountID, spec.NetworkID)
	_, known := m.privateNetworkFabric[spec.AccountID+"\x00"+spec.NetworkID]
	hostKnown := known
	if m.captureRunner != nil {
		_, err := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", host})
		hostKnown = err == nil
		_, err = m.captureRunner.RunCapture(ctx, []string{"ip", "-n", namespace, "link", "show", "dev", peer})
		known = hostKnown && err == nil
	}
	if !known {
		// A prior interrupted move may have left both endpoints in root.
		if hostKnown {
			if err := m.run.Run(ctx, []string{"ip", "link", "del", "dev", host}); err != nil {
				return err
			}
		}
		if err := m.run.Run(ctx, []string{"ip", "link", "add", "dev", host, "type", "veth", "peer", "name", peer}); err != nil {
			return err
		}
		if err := m.run.Run(ctx, []string{"ip", "link", "set", "dev", peer, "netns", namespace}); err != nil {
			return err
		}
	}
	mtu := m.privateNetworkMTU()
	if mtu == 0 {
		mtu = 1500
	}
	commands := [][]string{
		{"ip", "link", "set", "dev", host, "mtu", fmt.Sprint(mtu)},
		{"ip", "-n", namespace, "link", "set", "dev", peer, "mtu", fmt.Sprint(mtu)},
		{"ip", "link", "set", "dev", host, "master", plan.BridgeName},
		{"ip", "link", "set", "dev", host, "up"},
		{"ip", "-n", namespace, "addr", "replace", plan.GatewayCIDR.String(), "dev", peer},
		{"ip", "-n", namespace, "link", "set", "dev", peer, "up"},
	}
	for _, argv := range commands {
		if err := m.run.Run(ctx, argv); err != nil {
			return fmt.Errorf("reconcile private router endpoint: %w", err)
		}
	}
	return nil
}

// ReconcilePrivateNetworkPeerings replaces the complete account/region
// forwarding policy. Fabric setup must precede it on every regional node.
func (m *Manager) ReconcilePrivateNetworkPeerings(ctx context.Context, accountID, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	commands, err := privatenetwork.RouterPolicyCommands(accountID, region, networks, routes)
	if err != nil {
		return err
	}
	m.privateNetworkFabricMu.Lock()
	defer m.privateNetworkFabricMu.Unlock()
	if err := m.ensurePrivateRouterNamespaceLocked(ctx, accountID, region); err != nil {
		return err
	}
	for _, spec := range networks {
		plan, err := privatenetwork.BuildFabricPlan(spec)
		if err != nil {
			return err
		}
		// Do not acknowledge policy before all the local L3 endpoints exist.
		if err := m.reconcilePrivateRouterLinkLocked(ctx, plan); err != nil {
			return err
		}
	}
	return m.runAtomicNetworkPolicy(ctx, privatenetwork.RouterNamespace(accountID, region), commands)
}

func (m *Manager) removePrivateRouterLinkLocked(ctx context.Context, plan privatenetwork.FabricPlan) error {
	host := privatenetwork.RouterHostLink(plan.Spec.AccountID, plan.Spec.NetworkID)
	known := true
	if m.captureRunner != nil {
		_, err := m.captureRunner.RunCapture(ctx, []string{"ip", "link", "show", "dev", host})
		known = err == nil
	}
	if known {
		if err := m.run.Run(ctx, []string{"ip", "link", "del", "dev", host}); err != nil {
			return err
		}
	}
	if m.captureRunner != nil {
		namespace := privatenetwork.RouterNamespace(plan.Spec.AccountID, plan.Spec.Region)
		links, err := m.captureRunner.RunCapture(ctx, []string{"ip", "-n", namespace, "-o", "link", "show"})
		if err == nil && !strings.Contains(string(links), "gri-") {
			if err := m.run.Run(ctx, []string{"ip", "netns", "del", namespace}); err != nil {
				return err
			}
			delete(m.privateNetworkRouters, plan.Spec.AccountID+"\x00"+plan.Spec.Region)
		}
	}
	return nil
}

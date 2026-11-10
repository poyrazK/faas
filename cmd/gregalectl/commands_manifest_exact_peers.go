package main

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// exactPeers names every host one firewall list admits, each by its own
// address (ADR-20261110120000). An IP-literal host is a /32 here; a hostname
// host is its inventory name, which the nftables role resolves from the same
// private address /etc/hosts maps the fleet's names to. No provider IP is
// written into generated files. The *_allowed_cidrs lists stay as they are
// (the overlay subnet for hostname hosts) for every other consumer and as
// the firewall's fallback when a name does not resolve.
type exactPeers struct {
	cidrs []string
	hosts []string
}

func (p *exactPeers) add(h manifest.Host) error {
	address, _, err := manifest.ParseHostPort(h.Address)
	if err != nil {
		return fmt.Errorf("host %s firewall address: %w", h.Name, err)
	}
	if addr, err := netip.ParseAddr(address); err == nil {
		p.cidrs = append(p.cidrs, netip.PrefixFrom(addr, addr.BitLen()).String())
		return nil
	}
	p.hosts = append(p.hosts, h.Name)
	return nil
}

func (p exactPeers) render(key string) string {
	return fmt.Sprintf("%s: {cidrs: [%s], hosts: [%s]}\n", key, quotedYAMLList(p.cidrs), quotedYAMLList(p.hosts))
}

// renderExactPeerVars gives the control plane its compute hosts, and each
// compute host the control plane plus its compute peers. Compute peers reach
// each other only on the ports the firewall's compute-peer rule names:
// gatewayd-internal forwards to the owning node's vmmd and wakes through
// that node's schedd.
func renderExactPeerVars(hosts []manifest.Host, host manifest.Host) (string, error) {
	if host.Role != roleControlPlane && host.Role != roleComputeOnly {
		return "", nil
	}
	var controlPlane, compute, peers exactPeers
	for _, h := range hosts {
		var err error
		switch h.Role {
		case roleControlPlane:
			err = controlPlane.add(h)
		case roleComputeOnly:
			err = compute.add(h)
			if err == nil && h.Name != host.Name {
				err = peers.add(h)
			}
		}
		if err != nil {
			return "", err
		}
	}
	var b strings.Builder
	if host.Role == roleControlPlane {
		b.WriteString(compute.render("faas_compute_allowed_exact"))
		return b.String(), nil
	}
	b.WriteString(controlPlane.render("faas_control_plane_allowed_exact"))
	b.WriteString(peers.render("faas_compute_peer_exact"))
	return b.String(), nil
}

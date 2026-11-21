package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// renderDiskEncryptionVars carries ADR-20261109120000 to one host: whether its
// data volume is encrypted, the Tang servers it unlocks from (every other
// fleet host, never itself: its own Tang keys sit on the plain root disk
// beside the volume), the port its own Tang server listens on, the sources
// that may reach it, and the operator recipients its recovery key is sealed to.
func renderDiskEncryptionVars(m *manifest.Manifest, self manifest.Host) (string, error) {
	var b strings.Builder
	d := m.DiskEncryption
	if d == nil {
		b.WriteString("faas_disk_encryption: false\n")
		return b.String(), nil
	}
	port := strconv.Itoa(d.Port())
	listen, _, err := manifest.ParseHostPort(self.Address)
	if err != nil {
		return "", fmt.Errorf("host %s tang address: %w", self.Name, err)
	}
	// A peer whose manifest address is an IP is bound at that address. A peer
	// named by host name is listed by inventory name instead: the storage
	// role binds it at the private address the discovery play resolved
	// (tasks/resolve_private_address.yml). The private names are only in
	// /etc/hosts once the overlay role has run, later in the same bootstrap,
	// publicly they resolve elsewhere, and the bound URLs stay in the LUKS
	// header for good.
	var peers, peerHosts []string
	for _, host := range manifest.TangServers(m.Fleet.Hosts) {
		if host.Name == self.Name {
			continue
		}
		address, _, err := manifest.ParseHostPort(host.Address)
		if err != nil {
			return "", fmt.Errorf("host %s tang address: %w", host.Name, err)
		}
		if net.ParseIP(address) == nil {
			peerHosts = append(peerHosts, host.Name)
			continue
		}
		peers = append(peers, "http://"+net.JoinHostPort(address, port))
	}
	b.WriteString("faas_disk_encryption: true\n")
	fmt.Fprintf(&b, "faas_tang_port: %s\n", port)
	// An IP manifest address is the private one; on bare metal the default
	// route's address is the public card. A host name is left to the role
	// default, the discovered private address, for the same reason as above.
	if net.ParseIP(listen) != nil {
		fmt.Fprintf(&b, "faas_tang_listen_address: %q\n", listen)
	}
	fmt.Fprintf(&b, "faas_tang_peer_urls: [%s]\n", quotedYAMLList(peers))
	fmt.Fprintf(&b, "faas_tang_peer_hosts: [%s]\n", quotedYAMLList(peerHosts))
	fmt.Fprintf(&b, "faas_tang_allowed_cidrs: [%q]\n", m.Overlay.CIDR)
	fmt.Fprintf(&b, "faas_disk_recovery_recipients: [%s]\n", quotedYAMLList(d.RecoveryRecipients))
	return b.String(), nil
}

// renderStandbyDiskEncryptionVars gives the warm standby (outside the fleet,
// inside overlay.cidr) the fleet's Tang servers to unlock from. It serves no
// Tang itself.
func renderStandbyDiskEncryptionVars(m *manifest.Manifest, standby *manifest.PostgresStandby) (string, error) {
	// The standby's address is a bare IP; the port only satisfies the
	// host:port parsing of fleet addresses.
	return renderDiskEncryptionVars(m, manifest.Host{Name: standby.Name, Address: net.JoinHostPort(standby.Address, "5432")})
}

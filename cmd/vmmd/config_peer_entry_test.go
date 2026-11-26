// adr: 20261123100000 — vmmd's runtime host policy keeps the hairpin of
// guests' 443 calls to the other compute edges' entry addresses onto this
// node's own, as the boot policy does.
package main

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigPeerEntryAddrsHairpin(t *testing.T) {
	t.Setenv("FAAS_HOST_BRIDGE_CIDR", "10.100.0.0/16")
	t.Setenv("FAAS_PUBLIC_ENTRY_ADDRS", "203.0.113.10")
	t.Setenv("FAAS_PEER_ENTRY_ADDRS", "203.0.113.11,203.0.113.12")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rendered := runtimeHostPolicy(cfg.ComputeNode, netip.MustParsePrefix(cfg.ComputeNode.HostBridgeCIDR), false).Render()
	want := `ip daddr { 203.0.113.11, 203.0.113.12 } tcp dport 443 dnat ip to 203.0.113.10`
	if !strings.Contains(rendered, want) {
		t.Fatalf("runtime policy lacks %q:\n%s", want, rendered)
	}
}

func TestLoadConfigRejectsBadPeerEntryAddrs(t *testing.T) {
	for _, tc := range []struct {
		name, peers, want string
	}{
		{"not an address", "203.0.113.11,nope", "peer_entry_addrs[1]"},
		{"IPv6", "2001:db8::11", "plain IPv4 address"},
		{"own entry address", "203.0.113.10", "this node's own entry address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_PUBLIC_ENTRY_ADDRS", "203.0.113.10")
			t.Setenv("FAAS_PEER_ENTRY_ADDRS", tc.peers)
			if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadConfig err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// adr: 20261103163954 — vmmd's runtime host policy keeps a compute edge with
// no proxy in front open on 443 at its entry addresses only, as the boot
// policy does.
package main

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigPublicEntryAddrsOpenOnly443(t *testing.T) {
	t.Setenv("FAAS_HOST_BRIDGE_CIDR", "10.100.0.0/16")
	t.Setenv("FAAS_PUBLIC_ENTRY_ADDRS", "203.0.113.10, 2001:db8::10")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rendered := runtimeHostPolicy(cfg.ComputeNode, netip.MustParsePrefix(cfg.ComputeNode.HostBridgeCIDR), false).Render()
	for _, want := range []string{
		`ip daddr 203.0.113.10 tcp dport 443 accept comment "public entry"`,
		`ip6 daddr 2001:db8::10 tcp dport 443 accept comment "public entry"`,
		`ip daddr 203.0.113.10 drop comment "public entry carries 443 only"`,
		"tcp dport 22 accept",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime policy lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "tcp dport { 22,80,443 } accept") {
		t.Fatalf("runtime policy still admits 80/443 on every address:\n%s", rendered)
	}
}

// The drop-in sends Cloudflare's ranges beside the entry addresses for a vmmd
// that predates them; this one renders the entry rules.
func TestLoadConfigPublicEntryAddrsWinOverCloudflareSources(t *testing.T) {
	t.Setenv("FAAS_HOST_BRIDGE_CIDR", "10.100.0.0/16")
	t.Setenv("FAAS_PUBLIC_ENTRY_ADDRS", "203.0.113.10")
	t.Setenv("FAAS_PUBLIC_INGRESS_CIDRS", "173.245.48.0/20")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rendered := runtimeHostPolicy(cfg.ComputeNode, netip.MustParsePrefix(cfg.ComputeNode.HostBridgeCIDR), false).Render()
	if !strings.Contains(rendered, `ip daddr 203.0.113.10 tcp dport 443 accept comment "public entry"`) || strings.Contains(rendered, "gateway via Cloudflare") {
		t.Fatalf("runtime policy did not put the entry rules first:\n%s", rendered)
	}
}

func TestLoadConfigRejectsBadPublicEntryAddrs(t *testing.T) {
	for _, tc := range []struct {
		name, entry, want string
	}{
		{"not an address", "203.0.113.10,not-an-addr", "public_entry_addrs[1]"},
		{"not canonical", "2001:0db8::10", "canonical form (2001:db8::10)"},
		{"IPv4-mapped", "::ffff:203.0.113.10", "canonical form (203.0.113.10)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_PUBLIC_ENTRY_ADDRS", tc.entry)
			if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadConfig err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

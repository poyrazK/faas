// adr: 20261102081618 — vmmd's runtime host policy keeps a compute edge that
// Cloudflare reaches directly Cloudflare-only, as the boot policy does.
package main

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigPublicIngressCIDRsRestrictTheWebPorts(t *testing.T) {
	t.Setenv("FAAS_HOST_BRIDGE_CIDR", "10.100.0.0/16")
	t.Setenv("FAAS_PUBLIC_INGRESS_CIDRS", "173.245.48.0/20, 2400:cb00::/32")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rendered := runtimeHostPolicy(cfg.ComputeNode, netip.MustParsePrefix(cfg.ComputeNode.HostBridgeCIDR), false).Render()
	for _, want := range []string{
		"tcp dport 22 accept",
		`ip saddr 173.245.48.0/20 tcp dport { 80,443 } accept comment "gateway via Cloudflare"`,
		`ip6 saddr 2400:cb00::/32 tcp dport { 80,443 } accept comment "gateway via Cloudflare"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime policy lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "tcp dport { 22,80,443 } accept") {
		t.Fatalf("runtime policy still admits 80/443 from anywhere:\n%s", rendered)
	}
}

func TestLoadConfigRejectsAnInvalidPublicIngressCIDR(t *testing.T) {
	t.Setenv("FAAS_PUBLIC_INGRESS_CIDRS", "173.245.48.0/20,not-a-prefix")
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil || !strings.Contains(err.Error(), "public_ingress_cidrs[1]") {
		t.Fatalf("LoadConfig err = %v, want public_ingress_cidrs[1] rejected", err)
	}
}

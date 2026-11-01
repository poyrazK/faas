// adr: 1027 — the database route switch reaches the runtime host policy on
// the host's own bridge, so wake-time rebuilds keep the database NAT.
package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDatabaseRouteConfiguresRuntimePolicy(t *testing.T) {
	t.Setenv("FAAS_HOST_BRIDGE_CIDR", "10.123.0.0/16")
	dir := t.TempDir()
	cfg, err := LoadConfig(filepath.Join(dir, "missing.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	bridge := netip.MustParsePrefix(cfg.ComputeNode.HostBridgeCIDR)
	if off := runtimeHostPolicy(cfg.ComputeNode, bridge, false); off.DatabaseRoute != nil {
		t.Fatal("the database route is on without database_route_enabled")
	}

	path := filepath.Join(dir, "vmmd.toml")
	if err := os.WriteFile(path, []byte("[compute_node]\ndatabase_route_enabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(path); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.ComputeNode.DatabaseRouteEnabled {
		t.Fatal("database_route_enabled did not load")
	}
	rendered := runtimeHostPolicy(cfg.ComputeNode, bridge, false).Render()
	if !strings.Contains(rendered, `iifname "br-tenants" ip daddr 198.18.0.1 tcp dport 5432 dnat ip to 10.123.0.1:10083`) ||
		!strings.Contains(rendered, "ip daddr 198.18.0.1 drop") {
		t.Fatalf("runtime policy lacks the database route on the host bridge:\n%s", rendered)
	}
}

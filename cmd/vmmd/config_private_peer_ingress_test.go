// adr: 20261110120000 — vmmd's runtime host policy admits the control plane
// and the compute peers by exact address, as the boot policy does.
package main

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

func privateRuleLines(rendered string) []string {
	var out []string
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "saddr 10.42.") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestRuntimePolicyExactPrivateIngress(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		want, not []string
	}{
		{
			name: "pre-ADR drop-in keeps the subnet",
			env:  map[string]string{"FAAS_PRIVATE_INGRESS_CIDRS": "10.42.0.0/24", "FAAS_PRIVATE_INGRESS_TCP_PORTS": "50051,9100"},
			want: []string{"10.42.0.0/24"},
		},
		{
			name: "exact control plane and peers",
			env: map[string]string{
				"FAAS_PRIVATE_INGRESS_CIDRS": "10.42.0.0/24", "FAAS_PRIVATE_INGRESS_TCP_PORTS": "50051,9100",
				"FAAS_PRIVATE_CONTROL_INGRESS_CIDRS": "10.42.0.1/32",
				"FAAS_PRIVATE_PEER_INGRESS_CIDRS":    "10.42.0.3/32,10.42.0.4/32", "FAAS_PRIVATE_PEER_INGRESS_TCP_PORTS": "50051,9091",
			},
			want: []string{"10.42.0.1/32", "10.42.0.3/32", "10.42.0.4/32"},
			not:  []string{"10.42.0.0/24"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_HOST_BRIDGE_CIDR", "10.100.0.0/16")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			rules := privateRuleLines(runtimeHostPolicy(cfg.ComputeNode, netip.MustParsePrefix(cfg.ComputeNode.HostBridgeCIDR), false).Render())
			joined := strings.Join(rules, "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Errorf("runtime policy does not admit %s:\n%s", want, joined)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(joined, not) {
					t.Errorf("runtime policy still admits %s:\n%s", not, joined)
				}
			}
			for _, line := range rules {
				if strings.Contains(line, "10.42.0.3/32") && (strings.Contains(line, "9100") || !strings.Contains(line, "9091")) {
					t.Errorf("peer admitted to the wrong ports: %s", line)
				}
			}
		})
	}
}

func TestLoadConfigPeerIngressNeedsPorts(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_PEER_INGRESS_CIDRS", "10.42.0.3/32")
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil || !strings.Contains(err.Error(), "set together") {
		t.Fatalf("LoadConfig err = %v, want peer sources without ports rejected", err)
	}
}

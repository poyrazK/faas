package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// The manifest's hot_path switches reach the compute nodes' roles as host
// vars, and never the control plane's. The local vmmd socket renders both
// halves together.
func TestRenderManifestAnsibleFiles_HotPath(t *testing.T) {
	base := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n", 1)
	all := []string{
		"faas_request_id_journal_local: true\n",
		"faas_request_telemetry_minute_collapse: true\n",
		"faas_vmmd_local_data_socket: true\n",
		"faas_gateway_local_vmmd_socket: true\n",
		"faas_internal_keepalive_per_node: true\n",
		"faas_edge_internal_hop_binary: true\n",
		"faas_schedd_frozen_tier: true\n",
		"faas_schedd_frozen_fast_path: true\n",
		"faas_schedd_owner_cache: true\n",
	}
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{name: "off", yaml: base},
		{name: "local socket renders both halves", yaml: base + "hot_path:\n  local_vmmd_data_socket: true\n",
			want: []string{"faas_vmmd_local_data_socket: true\n", "faas_gateway_local_vmmd_socket: true\n"}},
		{name: "all on", yaml: base + "public_edge:\n  compute_nodes: true\n" +
			"hot_path:\n  local_request_id_journal: true\n  request_telemetry_minute_collapse: true\n  local_vmmd_data_socket: true\n" +
			"  edge_keepalive_per_node: true\n  edge_binary_hop: true\n  frozen_tier: true\n  frozen_fast_path: true\n  owner_cache: true\n", want: all},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := manifest.Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("manifest.Parse: %v", err)
			}
			if errs := m.Validate(); errs != nil {
				t.Fatalf("manifest.Validate: %v", errs)
			}
			files, err := renderManifestAnsibleFiles(m, t.TempDir())
			if err != nil {
				t.Fatalf("renderManifestAnsibleFiles: %v", err)
			}
			var controlVars, computeVars string
			for _, file := range files {
				switch {
				case strings.HasSuffix(file.Path, "fsn-1.yml"):
					controlVars = string(file.Body)
				case strings.HasSuffix(file.Path, "fsn-2.yml"):
					computeVars = string(file.Body)
				}
			}
			for _, line := range all {
				want := false
				for _, w := range tc.want {
					want = want || w == line
				}
				if got := strings.Contains(computeVars, line); got != want {
					t.Errorf("compute host has %q = %v, want %v:\n%s", line, got, want, computeVars)
				}
				if strings.Contains(controlVars, line) {
					t.Errorf("%q rendered on the control plane:\n%s", line, controlVars)
				}
			}
		})
	}
}

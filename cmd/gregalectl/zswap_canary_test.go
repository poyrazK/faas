package main

// adr: 906 decision 6 — production turns zswap on for named canary compute
// nodes only, never fleet-wide, with a swapfile the nodes' root filesystems
// can hold.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestZswapIsACanaryOnProductionComputeNodes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "group_vars", "compute_nodes", "zswap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := vars["faas_zswap_enabled"].(string); !strings.Contains(enabled, "in faas_zswap_canary_nodes") {
		t.Errorf("faas_zswap_enabled = %v, want it limited to faas_zswap_canary_nodes", vars["faas_zswap_enabled"])
	}
	canary, _ := vars["faas_zswap_canary_nodes"].([]any)
	if len(canary) == 0 {
		t.Errorf("faas_zswap_canary_nodes = %v, want named canary nodes", vars["faas_zswap_canary_nodes"])
	}
	seen := map[string]bool{}
	for _, n := range canary {
		name, _ := n.(string)
		if !strings.HasSuffix(name, ".faas") || seen[name] {
			t.Errorf("faas_zswap_canary_nodes = %v, want distinct canonical node names", canary)
		}
		seen[name] = true
	}
	// Production compute roots have ~9 GB free; a swapfile cannot be sparse.
	size, _ := vars["faas_zswap_swapfile_size_mb"].(int)
	if size <= 0 || size > 4096 {
		t.Errorf("faas_zswap_swapfile_size_mb = %v, want at most 4096", vars["faas_zswap_swapfile_size_mb"])
	}
}

package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// computeKeyRetirement is which shared keys the operator has taken off
// compute nodes (ADR-20261109170000 sign.key; seal-domains-rollout
// fleet.age). node_join.yml reads the same switches and removes a retired key
// instead of staging it.
type computeKeyRetirement struct {
	signKey  bool
	fleetKey bool
}

// retiredComputeKeys resolves faas_compute_sign_key_retired and
// faas_compute_fleet_key_retired the way the join play sees them. The
// manifest renders the sign switch from artifact_signer.retire_compute_key
// into the inventory (manifestSignRetired); the join's --ansible-vars-file is
// passed with -e, so a value there wins over the inventory. Values follow
// Ansible's bool filter, so the CLI and the play agree on "retired".
func retiredComputeKeys(varsFile string, manifestSignRetired bool) (computeKeyRetirement, error) {
	retired := computeKeyRetirement{signKey: manifestSignRetired}
	if strings.TrimSpace(varsFile) == "" {
		return retired, nil
	}
	body, err := os.ReadFile(varsFile)
	if err != nil {
		return computeKeyRetirement{}, fmt.Errorf("--ansible-vars-file: %w", err)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(body, &vars); err != nil {
		return computeKeyRetirement{}, fmt.Errorf("--ansible-vars-file: parse: %w", err)
	}
	if v, ok := vars["faas_compute_sign_key_retired"]; ok {
		retired.signKey = ansibleBool(v)
	}
	retired.fleetKey = ansibleBool(vars["faas_compute_fleet_key_retired"])
	return retired, nil
}

// ansibleBool mirrors Ansible's `| bool` for the values a vars file holds.
func ansibleBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t == 1
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on", "y", "t":
			return true
		}
	}
	return false
}

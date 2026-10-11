package daemonunitspec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestComputeUnitsAreEnabledByJoinOrOperator pins the compute daemon set to
// what a compute rollout actually enables. rc.249's production-us rollout
// failed on fsn-2: faas-profiled joined faas_compute_only_units, its role
// stages the unit disabled for an operator to enable, node_join never enables
// it, and strict fleet_verify then failed on "faas-profiled.service is not
// enabled". Every compute unit must be enabled by node_join or be listed in
// faas_operator_enabled_units, never both.
func TestComputeUnitsAreEnabledByJoinOrOperator(t *testing.T) {
	root := repoRoot(t)
	var daemons struct {
		ComputeOnlyUnits []string `yaml:"faas_compute_only_units"`
		OperatorEnabled  []string `yaml:"faas_operator_enabled_units"`
	}
	readYAML(t, filepath.Join(root, "deploy", "ansible", "vars", "daemons.yml"), &daemons)

	var plays []struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	readYAML(t, filepath.Join(root, "deploy", "ansible", "node_join.yml"), &plays)
	var joinEnabled []string
	for _, play := range plays {
		for _, task := range flattenTasks(play.Tasks) {
			if task["name"] != "Enable and restart the compute-only daemon set" {
				continue
			}
			loop, _ := task["loop"].([]any)
			for _, unit := range loop {
				joinEnabled = append(joinEnabled, unit.(string))
			}
		}
	}
	if len(joinEnabled) == 0 {
		t.Fatal(`node_join.yml has no "Enable and restart the compute-only daemon set" loop`)
	}

	for _, unit := range daemons.ComputeOnlyUnits {
		byJoin := slices.Contains(joinEnabled, unit)
		byOperator := slices.Contains(daemons.OperatorEnabled, unit)
		switch {
		case !byJoin && !byOperator:
			t.Errorf("%s is a compute unit that node_join never enables; enable it there or list it in faas_operator_enabled_units", unit)
		case byJoin && byOperator:
			t.Errorf("%s is enabled by node_join and also listed as operator-enabled", unit)
		}
	}
	for _, unit := range daemons.OperatorEnabled {
		if !slices.Contains(daemons.ComputeOnlyUnits, unit) {
			t.Errorf("faas_operator_enabled_units lists %s, which is not a compute unit", unit)
		}
	}

	verify, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "fleet_verify", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(verify), "item.item not in (faas_operator_enabled_units") {
		t.Error("fleet_verify's strict enablement check must skip faas_operator_enabled_units")
	}
}

func readYAML(t *testing.T, path string, into any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// flattenTasks returns tasks with block/rescue/always children inlined.
func flattenTasks(tasks []map[string]any) []map[string]any {
	var out []map[string]any
	for _, task := range tasks {
		out = append(out, task)
		for _, key := range []string{"block", "rescue", "always"} {
			children, _ := task[key].([]any)
			var nested []map[string]any
			for _, child := range children {
				if m, ok := child.(map[string]any); ok {
					nested = append(nested, m)
				}
			}
			out = append(out, flattenTasks(nested)...)
		}
	}
	return out
}

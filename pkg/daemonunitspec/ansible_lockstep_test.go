package daemonunitspec

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// daemonsYAML mirrors deploy/ansible/vars/daemons.yml.
type daemonsYAML struct {
	ControlPlaneUnits []string `yaml:"faas_control_plane_units"`
	ComputeOnlyUnits  []string `yaml:"faas_compute_only_units"`
	LegacyUnits       []string `yaml:"faas_legacy_units"`
	Probes            map[string]struct {
		Probe     string `yaml:"probe"`
		Target    string `yaml:"target"`
		ReadyzURL string `yaml:"readyz_url"`
	} `yaml:"faas_daemon_probes"`
}

// TestDaemonsYAML_LockstepWithRegistry pins deploy/ansible/vars/daemons.yml
// (consumed by role_convergence + fleet_verify) to the Registry: the
// per-role unit sets and the readiness probes must match exactly. Adding a
// daemon, moving it between roles, or changing its probe in Go without
// updating the YAML fails here (ADR-143).
func TestDaemonsYAML_LockstepWithRegistry(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "vars", "daemons.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var got daemonsYAML
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse daemons.yml: %v", err)
	}

	units := func(role Role) []string {
		var out []string
		for _, n := range DaemonsForRole(role) {
			out = append(out, "faas-"+n+".service")
		}
		sort.Strings(out)
		return out
	}
	sortedCopy := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.Strings(out)
		return out
	}
	if want := units(RoleControlPlane); !reflect.DeepEqual(sortedCopy(got.ControlPlaneUnits), want) {
		t.Errorf("faas_control_plane_units = %v, Registry says %v", got.ControlPlaneUnits, want)
	}
	if want := units(RoleComputeOnly); !reflect.DeepEqual(sortedCopy(got.ComputeOnlyUnits), want) {
		t.Errorf("faas_compute_only_units = %v, Registry says %v", got.ComputeOnlyUnits, want)
	}
	if len(got.LegacyUnits) == 0 {
		t.Errorf("faas_legacy_units must list the retired one-box units role_convergence masks")
	}

	for _, e := range Registry {
		unit := "faas-" + e.Name + ".service"
		p, ok := got.Probes[unit]
		if !ok {
			t.Errorf("faas_daemon_probes missing %s", unit)
			continue
		}
		if p.Probe != string(e.Lifecycle.Probe) || p.Target != e.Lifecycle.ProbeTarget || p.ReadyzURL != e.Lifecycle.ReadyzURL {
			t.Errorf("%s probe = %s %q readyz=%q, Registry says %s %q readyz=%q", unit, p.Probe, p.Target, p.ReadyzURL, e.Lifecycle.Probe, e.Lifecycle.ProbeTarget, e.Lifecycle.ReadyzURL)
		}
		if e.Role != RoleControlPlane && e.Role != RoleComputeOnly {
			t.Errorf("%s: Registry entry has no Role", e.Name)
		}
	}
	for unit := range got.Probes {
		found := false
		for _, e := range Registry {
			if unit == "faas-"+e.Name+".service" {
				found = true
			}
		}
		if !found {
			t.Errorf("faas_daemon_probes lists %s which is not a Registry daemon", unit)
		}
	}
}

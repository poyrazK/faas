package daemonunitspec_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// releaseUnits mirrors scripts/ci/release_units.json, the lane table the
// per-unit release lane reads (docs/adr/1650-per-unit-control-plane-releases.md).
type releaseUnits struct {
	Binaries map[string]struct {
		Lane     string `json:"lane"`
		Services []struct {
			Name     string `json:"name"`
			Optional bool   `json:"optional"`
			Readyz   string `json:"readyz"`
		} `json:"services"`
	} `json:"binaries"`
}

// TestReleaseUnitLanesMatchTheRegistry: the unit lane restarts the services
// its lane table names and gates them on the readiness URLs it lists. A
// daemon that gains a compute role, a new unit of an existing binary, or a
// moved readiness port must update the table, or the lane would deploy a
// shared binary to the control plane alone, miss a unit, or probe a dead
// port.
func TestReleaseUnitLanesMatchTheRegistry(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "release_units.json"))
	if err != nil {
		t.Fatalf("read lane table: %v", err)
	}
	var table releaseUnits
	if err := json.Unmarshal(body, &table); err != nil {
		t.Fatalf("decode lane table: %v", err)
	}

	type service struct {
		optional bool
		readyz   string
	}
	roles := map[string]map[daemonunitspec.Role]bool{}
	services := map[string]map[string]service{}
	optional := map[string]bool{}
	for _, entry := range daemonunitspec.OptionalRegistry {
		optional[entry.Name] = true
	}
	for _, entry := range daemonunitspec.UnitEntries() {
		exec := strings.Fields(entry.Unit().ExecStart)
		if len(exec) == 0 || !strings.HasPrefix(exec[0], "/opt/faas/current/bin/") {
			t.Fatalf("%s: ExecStart %q does not run a release binary", entry.Name, entry.Unit().ExecStart)
		}
		binary := strings.TrimPrefix(exec[0], "/opt/faas/current/bin/")
		if roles[binary] == nil {
			roles[binary] = map[daemonunitspec.Role]bool{}
			services[binary] = map[string]service{}
		}
		roles[binary][entry.Role] = true
		for _, role := range entry.AdditionalRoles {
			roles[binary][role] = true
		}
		if entry.Role == daemonunitspec.RoleControlPlane {
			services[binary][entry.Name] = service{optional: optional[entry.Name], readyz: entry.Lifecycle.ReadyzURL}
		}
	}

	for binary, have := range roles {
		entry, ok := table.Binaries[binary]
		if !ok {
			t.Errorf("%s runs as a systemd unit but has no lane in release_units.json", binary)
			continue
		}
		want := "compute"
		switch {
		case have[daemonunitspec.RoleControlPlane] && have[daemonunitspec.RoleComputeOnly]:
			want = "shared"
		case have[daemonunitspec.RoleControlPlane]:
			want = "control-plane"
		}
		if entry.Lane != want {
			t.Errorf("%s: lane %q, registry roles make it %q", binary, entry.Lane, want)
		}
		if want != "control-plane" {
			continue
		}
		got := map[string]service{}
		for _, s := range entry.Services {
			got[s.Name] = service{optional: s.Optional, readyz: s.Readyz}
		}
		for name, s := range services[binary] {
			if got[name] != s {
				t.Errorf("%s: service %s is %+v in the lane table, registry says %+v", binary, name, got[name], s)
			}
		}
		for name := range got {
			if _, ok := services[binary][name]; !ok {
				t.Errorf("%s: lane table lists service %s, which the registry does not run from it", binary, name)
			}
		}
	}

	// A binary with no systemd unit can still be a compute helper (the vmmd
	// bridges) or a CLI, but never a control-plane unit: the lane would have
	// nothing to restart.
	var unitless []string
	for binary, entry := range table.Binaries {
		if _, ok := roles[binary]; !ok && (entry.Lane == "control-plane" || entry.Lane == "shared") {
			unitless = append(unitless, binary)
		}
	}
	sort.Strings(unitless)
	if len(unitless) > 0 {
		t.Errorf("lane table puts binaries without a registry unit on the control plane: %v", unitless)
	}
}

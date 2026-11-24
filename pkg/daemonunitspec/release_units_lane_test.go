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

// edgeBinaries are the edge lane: the gateways, which roll host by host
// without a drain. vmmd and compute schedd ride the compute lane
// (docs/adr/20261123122457-compute-unit-lane.md), behind vmmd's adoption proof.
var edgeBinaries = map[string]bool{"gatewayd-public": true, "gatewayd-internal": true}

// releaseUnits mirrors scripts/ci/release_units.json, the lane table the
// per-unit release lane reads (docs/adr/1650-per-unit-control-plane-releases.md).
type releaseUnits struct {
	Binaries map[string]struct {
		Lane     string             `json:"lane"`
		Services []laneTableService `json:"services"`
	} `json:"binaries"`
	ComputeLane struct {
		Held []string `json:"held"`
	} `json:"compute_lane"`
}

type laneTableService struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Optional bool   `json:"optional"`
	Readyz   string `json:"readyz"`
}

// laneService is one registry unit of a binary on one host role.
type laneService struct {
	optional bool
	readyz   string
}

// checkComputeLaneServices: a shared or compute binary the compute lane
// deploys (docs/adr/20261123122457-compute-unit-lane.md) lists exactly the
// registry units that run it on the control plane and compute nodes, with
// their readiness URLs; a held one (database nodes, the agent egress host)
// lists none, because no lane restarts it.
func checkComputeLaneServices(t *testing.T, binary string, table []laneTableService, registry map[string]laneService, held []string) {
	t.Helper()
	for _, h := range held {
		if h == binary {
			if len(table) > 0 {
				t.Errorf("%s is held for the full release but lists services %+v", binary, table)
			}
			return
		}
	}
	got := map[string]laneService{}
	for _, s := range table {
		host := s.Host
		if host == "" {
			host = string(daemonunitspec.RoleControlPlane)
		}
		got[s.Name+"@"+host] = laneService{optional: s.Optional, readyz: s.Readyz}
	}
	for key, s := range registry {
		if got[key] != s {
			t.Errorf("%s: service %s is %+v in the lane table, registry says %+v", binary, key, got[key], s)
		}
	}
	for key := range got {
		if _, ok := registry[key]; !ok {
			t.Errorf("%s: lane table lists service %s, which the registry does not run from it", binary, key)
		}
	}
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
		host     daemonunitspec.Role
		optional bool
		readyz   string
	}
	roles := map[string]map[daemonunitspec.Role]bool{}
	services := map[string]map[string]service{}
	perRole := map[string]map[string]laneService{}
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
		services[binary][entry.Name] = service{host: entry.Role, optional: optional[entry.Name], readyz: entry.Lifecycle.ReadyzURL}
		// The compute lane restarts a shared daemon on every host role
		// that runs it (schedd on the control plane and compute nodes).
		if perRole[binary] == nil {
			perRole[binary] = map[string]laneService{}
		}
		for _, role := range append([]daemonunitspec.Role{entry.Role}, entry.AdditionalRoles...) {
			if role == daemonunitspec.RoleControlPlane || role == daemonunitspec.RoleComputeOnly {
				perRole[binary][entry.Name+"@"+string(role)] = laneService{optional: optional[entry.Name], readyz: entry.Lifecycle.ReadyzURL}
			}
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
		case edgeBinaries[binary]:
			want = "edge"
		case have[daemonunitspec.RoleControlPlane] && have[daemonunitspec.RoleComputeOnly]:
			want = "shared"
		case have[daemonunitspec.RoleControlPlane]:
			want = "control-plane"
		}
		if entry.Lane != want {
			t.Errorf("%s: lane %q, registry roles make it %q", binary, entry.Lane, want)
		}
		if want == "shared" || want == "compute" {
			checkComputeLaneServices(t, binary, entry.Services, perRole[binary], table.ComputeLane.Held)
			continue
		}
		// The control-plane lane deploys to the control plane only; the
		// edge lane to every host role that runs the binary.
		expected := map[string]service{}
		for name, s := range services[binary] {
			if want == "edge" || s.host == daemonunitspec.RoleControlPlane {
				expected[name] = s
			}
		}
		got := map[string]service{}
		for _, s := range entry.Services {
			host := daemonunitspec.Role(s.Host)
			if host == "" {
				host = daemonunitspec.RoleControlPlane
			}
			got[s.Name] = service{host: host, optional: s.Optional, readyz: s.Readyz}
		}
		for name, s := range expected {
			if got[name] != s {
				t.Errorf("%s: service %s is %+v in the lane table, registry says %+v", binary, name, got[name], s)
			}
		}
		for name := range got {
			if _, ok := expected[name]; !ok {
				t.Errorf("%s: lane table lists service %s, which the registry does not run from it", binary, name)
			}
		}
	}

	// A binary with no systemd unit can still be a compute helper (the vmmd
	// bridges) or a CLI, but never a control-plane unit: the lane would have
	// nothing to restart.
	var unitless []string
	for binary, entry := range table.Binaries {
		if _, ok := roles[binary]; !ok && (entry.Lane == "control-plane" || entry.Lane == "shared" || entry.Lane == "edge") {
			unitless = append(unitless, binary)
		}
	}
	sort.Strings(unitless)
	if len(unitless) > 0 {
		t.Errorf("lane table puts binaries without a registry unit on the control plane: %v", unitless)
	}
}

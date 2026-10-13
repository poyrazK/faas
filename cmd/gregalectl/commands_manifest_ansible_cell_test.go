// adr: 1152
// commands_manifest_ansible_cell_test.go pins the inventory for an ADR-1152 §2
// cell: three control nodes, one per zone. Each control node gets its own
// scheduler and Postgres addresses (not the last control node's), zones reach
// the host vars, and compute gateways reach apid through apid.faas.

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// adr: 1152
func TestRenderManifestAnsibleFiles_ThreeControlNodesGetTheirOwnAddresses(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:7100\n      zone: us-east1-b\n"+
			"    - name: fsn-4\n      role: control-plane\n      address: fsn-4.gregale.dev:7100\n      zone: us-east1-c\n"+
			"    - name: fsn-5\n      role: control-plane\n      address: fsn-5.gregale.dev:7100\n      zone: us-east1-d\n"+
			"    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n      zone: us-east1-b\n", 1)
	m, err := manifest.Parse([]byte(yaml))
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
	vars := map[string]string{}
	var inventory string
	for _, file := range files {
		if strings.HasSuffix(file.Path, filepath.Join("inventory", "hosts.ini")) {
			inventory = string(file.Body)
			continue
		}
		vars[strings.TrimSuffix(filepath.Base(file.Path), ".yml")] = string(file.Body)
	}
	if !strings.Contains(inventory, "[control_plane]\nfsn-1\nfsn-4\nfsn-5\n") {
		t.Errorf("inventory did not keep all three control nodes:\n%s", inventory)
	}
	for _, cp := range []struct{ name, zone string }{{"fsn-1", "us-east1-b"}, {"fsn-4", "us-east1-c"}, {"fsn-5", "us-east1-d"}} {
		body := vars[cp.name]
		for _, want := range []string{
			`faas_host_zone: "` + cp.zone + `"`,
			`faas_postgres_listen_addresses: "` + cp.name + `.gregale.dev"`,
			// meterd reaches whichever control node runs the active
			// central schedd: every control node registers schedd.faas and
			// standbys do not listen, so the dialer moves on to the holder.
			`faas_meterd_schedd_socket: "tcp://schedd.faas:7100"`,
			`names: ["` + cp.name + `.gregale.dev", "schedd.faas", "apid.faas"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s host vars missing %s:\n%s", cp.name, want, body)
			}
		}
	}
	if !strings.Contains(vars["fsn-2"], `faas_gatewayd_apid_loopback: "http://apid.faas:8081"`) {
		t.Errorf("compute gateway is pinned to one control node instead of apid.faas:\n%s", vars["fsn-2"])
	}
	for name, body := range vars {
		if strings.Contains(body, "faas_postgres_ha_enabled") {
			t.Errorf("%s enables Postgres HA without manifest postgresql.ha:\n%s", name, body)
		}
	}

	// postgresql.ha switches every host to the Patroni cluster.
	m.PostgreSQL.HA = true
	files, err = renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles with postgresql.ha: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Path, ".yml") && !strings.Contains(string(file.Body), "faas_postgres_ha_enabled: true") {
			t.Errorf("%s does not enable Postgres HA under postgresql.ha", file.Path)
		}
	}
}

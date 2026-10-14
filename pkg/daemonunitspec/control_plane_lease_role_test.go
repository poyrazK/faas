// adr: 1237
// control_plane_lease_role_test.go pins ADR-1237 §4's rollout rule for the
// control-plane singleton leases: the daemons leave their gates off unless the
// variable is "1", and control_plane_service renders "1" only for a cell with
// more than one control node. A one-node production keeps its pre-lease
// behaviour, so the lease code can merge without changing it.

package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 1237
func TestControlPlaneLeaseDropInOnlyEnablesMultiNodeCells(t *testing.T) {
	root := repoRoot(t)
	role := filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service")
	tmpl, err := os.ReadFile(filepath.Join(role, "templates", "zz-faas-control-plane-lease.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `Environment={{ item.env }}={{ '1' if (groups['control_plane'] | default([]) | length) > 1 else '0' }}`; !strings.Contains(string(tmpl), want) {
		t.Fatalf("lease drop-in must render 1 only for more than one control node; want line %q", want)
	}

	want := map[string]string{
		"faas-schedd": "FAAS_SCHEDD_CENTRAL_LEASE",
		"faas-meterd": "FAAS_METERD_LEASE",
		"faas-apid":   "FAAS_APID_BACKGROUND_LEASE",
	}
	got := map[string]string{}
	for _, task := range flattenRoleTasks("", loadYAMLList(t, filepath.Join(role, "tasks", "main.yml"))) {
		args, ok := task.body["ansible.builtin.template"].(map[string]any)
		if !ok || args["src"] != "zz-faas-control-plane-lease.conf.j2" {
			continue
		}
		items, _ := task.body["loop"].([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			unit, _ := m["unit"].(string)
			env, _ := m["env"].(string)
			got[unit] = env
		}
	}
	for unit, env := range want {
		if got[unit] != env {
			t.Errorf("%s: lease drop-in sets %q, want %q", unit, got[unit], env)
		}
	}

	// Each daemon must read exactly the variable the drop-in sets, and treat
	// anything but "1" as off.
	for path, env := range map[string]string{
		"cmd/schedd/central_lease.go":       "FAAS_SCHEDD_CENTRAL_LEASE",
		"cmd/meterd/meterd_lease.go":        "FAAS_METERD_LEASE",
		"pkg/apid/apidserver/background.go": "FAAS_APID_BACKGROUND_LEASE",
	} {
		src, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), `"`+env+`"`) || !strings.Contains(string(src), `== "1"`) {
			t.Errorf("%s must read %s and enable the lease only on \"1\"", path, env)
		}
	}
}

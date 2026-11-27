package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSplitJoinRoles(t *testing.T) {
	if got := splitJoinRoles(""); got != nil {
		t.Errorf("splitJoinRoles(\"\") = %q, want nil", got)
	}
	if got := splitJoinRoles(" vmmd_service, ,geoip,"); !reflect.DeepEqual(got, []string{"vmmd_service", "geoip"}) {
		t.Errorf("splitJoinRoles = %q", got)
	}
	if got := joinRolesVar(nil); got == nil || len(got) != 0 {
		t.Errorf("joinRolesVar(nil) = %#v, want an empty list (every role)", got)
	}
}

func TestRequireComputeRolesChecksTheComputePlay(t *testing.T) {
	dir := t.TempDir()
	writeContractTree(t, dir, map[string]string{
		"bootstrap.yml": "- name: cp\n  hosts: control_plane\n  roles:\n    - role: postgres\n" +
			"- name: compute\n  hosts: compute_nodes\n  roles:\n    - role: vmmd_service\n    - role: geoip\n",
	})
	if err := requireComputeRoles(dir, nil); err != nil {
		t.Errorf("no roles: %v", err)
	}
	if err := requireComputeRoles(dir, []string{"vmmd_service", "geoip"}); err != nil {
		t.Errorf("compute roles: %v", err)
	}
	if err := requireComputeRoles(dir, []string{"postgres"}); err == nil || !strings.Contains(err.Error(), "does not run role") {
		t.Errorf("control-plane role = %v, want a refusal", err)
	}
	if err := requireComputeRoles(dir, []string{"../x"}); err == nil || !strings.Contains(err.Error(), "not a role name") {
		t.Errorf("bad name = %v, want a refusal", err)
	}
}

// The real compute play: every role it runs is one --roles accepts.
func TestRequireComputeRolesAcceptsEveryComputeRole(t *testing.T) {
	ansibleDir := filepath.Join("..", "..", "deploy", "ansible")
	body, err := os.ReadFile(filepath.Join(ansibleDir, "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, roles, err := computeBootstrapContract(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) < 30 {
		t.Fatalf("compute play runs %d roles, want the full compute play", len(roles))
	}
	if err := requireComputeRoles(ansibleDir, roles); err != nil {
		t.Error(err)
	}
}

func TestReadComputeRoleRecord(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if got, err := readComputeRoleRecord(""); err != nil || got != "" {
		t.Errorf("no file = %q, %v", got, err)
	}
	good := `{"format_version": 1, "group": "compute_nodes", "globals": "sha256:aa", "order": ["geoip"], "roles": {"geoip": "sha256:bb"}}` + "\n"
	if got, err := readComputeRoleRecord(write("good.json", good)); err != nil || got != good {
		t.Errorf("good record = %q, %v; want it byte for byte", got, err)
	}
	cp := strings.Replace(good, "compute_nodes", "control_plane", 1)
	if _, err := readComputeRoleRecord(write("cp.json", cp)); err == nil {
		t.Error("a control_plane record must be refused")
	}
	if _, err := readComputeRoleRecord(write("bad.json", "{")); err == nil {
		t.Error("invalid JSON must be refused")
	}
	if _, err := readComputeRoleRecord(write("empty.json", `{"group": "compute_nodes"}`)); err == nil {
		t.Error("a record without hashes must be refused")
	}
}

// node_join narrows convergence only on a managed host, and records the
// role hashes with the contract: never in preparation.
func TestNodeJoinSelectsRolesOnlyOnAManagedHost(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []map[string]any
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	var selection, record map[string]any
	for _, play := range plays {
		tasks, _ := play["tasks"].([]any)
		for _, item := range tasks {
			task, _ := item.(map[string]any)
			switch task["name"] {
			case "Select the compute roles this convergence applies":
				selection = task
			case "Persist the converged compute role hashes":
				record = task
			}
		}
	}
	if selection == nil || record == nil {
		t.Fatalf("node_join.yml lacks the role selection (%v) or the role record (%v)", selection != nil, record != nil)
	}
	facts, _ := selection["ansible.builtin.set_fact"].(map[string]any)
	expr, _ := facts["faas_join_converge_roles"].(string)
	// A manifest or operator-vars change since the record converges every
	// role: the role hashes cover repository content only (rc.364).
	for _, want := range []string{"faas_join_managed_host", "faas_join_converge_roles_requested", "else none",
		"faas_join_recorded_role_record", ".environment", "faas_join_environment_sha256"} {
		if !strings.Contains(expr, want) {
			t.Errorf("role selection %q must mention %q", expr, want)
		}
	}
	copyArgs, _ := record["ansible.builtin.copy"].(map[string]any)
	if copyArgs["dest"] != "/var/lib/faas/bootstrap/compute-roles.json" {
		t.Errorf("role record dest = %v", copyArgs["dest"])
	}
	when, _ := record["when"].([]any)
	joined := ""
	for _, c := range when {
		s, _ := c.(string)
		joined += s + "\n"
	}
	if !strings.Contains(joined, "!= 'prepare'") {
		t.Errorf("the role record must not be written by preparation; when = %q", joined)
	}
}

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// adr: 20261018133100

// convergeVarsOf runs convergeControlPlane with opts and returns the extra
// variables it handed ansible-playbook.
func convergeVarsOf(t *testing.T, opts deployConvergeControlPlaneOptions) (deployConvergeControlPlaneReport, map[string]any) {
	t.Helper()
	var vars map[string]any
	ansiblePlaybookRunner = func(_ context.Context, _ string, args []string) error {
		for i := range args {
			if args[i] == "-e" && strings.HasSuffix(args[i+1], "converge-vars.json") {
				body, err := os.ReadFile(strings.TrimPrefix(args[i+1], "@"))
				if err != nil {
					return err
				}
				return json.Unmarshal(body, &vars)
			}
		}
		return nil
	}
	report, err := convergeControlPlane(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return report, vars
}

func TestConvergeControlPlaneRolesModeConvergesOnlyThoseRoles(t *testing.T) {
	opts := convergeFixture(t)
	whole, wholeVars := convergeVarsOf(t, opts)
	for _, key := range []string{"faas_cp_converge_roles_requested", "faas_cp_record_contract", "faas_cp_environment_sha256"} {
		if _, ok := wholeVars[key]; ok {
			t.Errorf("a whole-play convergence (ADR-580) must not pass %s", key)
		}
	}
	if len(whole.Roles) != 0 || !strings.HasPrefix(whole.Environment, "sha256:") {
		t.Fatalf("whole-play report = %+v", whole)
	}

	opts.Roles = []string{"postgres_capacity", "grafana"}
	report, vars := convergeVarsOf(t, opts)
	if report.Contract != whole.Contract || report.Environment != whole.Environment {
		t.Fatalf("--roles must not change the contract or environment hash: %+v vs %+v", report, whole)
	}
	if got := strings.Join(report.Roles, ","); got != "postgres_capacity,grafana" {
		t.Errorf("report roles = %q", got)
	}
	requested, _ := vars["faas_cp_converge_roles_requested"].([]any)
	if len(requested) != 2 || requested[0] != "postgres_capacity" || requested[1] != "grafana" {
		t.Errorf("faas_cp_converge_roles_requested = %v", vars["faas_cp_converge_roles_requested"])
	}
	if vars["faas_cp_record_contract"] != false {
		t.Errorf("the lane records the contract after its SLO gate; vars = %v", vars)
	}
	if vars["faas_cp_environment_sha256"] != report.Environment {
		t.Errorf("environment = %v, want %s", vars["faas_cp_environment_sha256"], report.Environment)
	}
	if vars["faas_join_defer_service_handlers"] != true {
		t.Errorf("the lane restarts FaaS daemons itself, one at a time; vars = %v", vars)
	}
}

func TestConvergeControlPlaneRolesModeRefusesUnknownRoles(t *testing.T) {
	for _, tt := range []struct {
		roles []string
		want  string
	}{
		{roles: []string{"vmmd_service"}, want: `does not run role "vmmd_service"`},
		{roles: []string{"postgres", "no_such_role"}, want: `does not run role "no_such_role"`},
	} {
		opts := convergeFixture(t)
		opts.Roles = tt.roles
		if _, err := convergeControlPlane(context.Background(), opts); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("roles %v: err = %v, want %q", tt.roles, err, tt.want)
		}
	}
	opts := convergeFixture(t)
	opts.Roles = []string{"../postgres"}
	if err := opts.validate(); err == nil || !strings.Contains(err.Error(), "not a role name") {
		t.Errorf("validate() = %v, want a role name error", err)
	}
}

func TestControlPlaneEnvironmentHashCoversInventoryAndVarsOnly(t *testing.T) {
	dir := t.TempDir()
	vars := filepath.Join(dir, "vars.yml")
	writeContractTree(t, dir, map[string]string{"vars.yml": "a: 1\n"})
	inventory := []manifestAnsibleFile{{Path: filepath.Join(dir, "inventory", "hosts.ini"), Body: []byte("[control_plane]\ncp\n")}}
	base, err := controlPlaneEnvironmentHash(dir, inventory, vars)
	if err != nil {
		t.Fatal(err)
	}
	// The temporary directory is not an input.
	moved := []manifestAnsibleFile{{Path: filepath.Join(dir, "x", "inventory", "hosts.ini"), Body: inventory[0].Body}}
	if got, _ := controlPlaneEnvironmentHash(filepath.Join(dir, "x"), moved, vars); got != base {
		t.Errorf("hash depends on the temporary directory")
	}
	changed := []manifestAnsibleFile{{Path: inventory[0].Path, Body: []byte("[control_plane]\ncp2\n")}}
	if got, _ := controlPlaneEnvironmentHash(dir, changed, vars); got == base {
		t.Errorf("an inventory change must change the environment hash")
	}
	writeContractTree(t, dir, map[string]string{"vars.yml": "a: 2\n"})
	if got, _ := controlPlaneEnvironmentHash(dir, inventory, vars); got == base {
		t.Errorf("an operator variable change must change the environment hash")
	}
}

// Every control-plane role entry carries the selection condition, so a
// --roles convergence skips exactly the roles it did not name; the compute
// play, which node_join imports, carries none.
func TestBootstrapControlPlaneRolesCarryTheSelection(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []map[string]any
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	selection := regexp.MustCompile(`^faas_cp_converge_roles \| default\(none\) is none or '([a-z0-9_]+)' in faas_cp_converge_roles$`)
	sawControlPlane := false
	for _, play := range plays {
		hosts, _ := play["hosts"].(string)
		roles, _ := play["roles"].([]any)
		for _, item := range roles {
			entry, _ := item.(map[string]any)
			name, _ := entry["role"].(string)
			var conditions []string
			switch when := entry["when"].(type) {
			case string:
				conditions = []string{when}
			case []any:
				for _, c := range when {
					s, _ := c.(string)
					conditions = append(conditions, s)
				}
			}
			var selected []string
			for _, c := range conditions {
				if m := selection.FindStringSubmatch(c); m != nil {
					selected = append(selected, m[1])
				} else if strings.Contains(c, "faas_cp_converge_roles") {
					t.Errorf("%s: role %s has a malformed selection condition %q", hosts, name, c)
				}
			}
			switch hosts {
			case "control_plane":
				sawControlPlane = true
				if len(selected) != 1 || selected[0] != name {
					t.Errorf("control-plane role %s must carry its own selection condition; conditions = %q", name, conditions)
				}
			default:
				if len(selected) != 0 {
					t.Errorf("%s role %s must not carry the control-plane selection", hosts, name)
				}
			}
		}
	}
	if !sawControlPlane {
		t.Fatal("bootstrap.yml has no control_plane play")
	}
}

func TestControlPlaneConvergePlaybookLaneModeRecordsNothing(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", controlPlaneConvergePlaybook))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	selectRoles := strings.Index(playbook, "faas_cp_converge_roles: >-")
	imported := strings.Index(playbook, "ansible.builtin.import_playbook: bootstrap.yml")
	laneEnd := strings.Index(playbook, "when: not (faas_cp_record_contract | default(true) | bool)")
	persist := strings.Index(playbook, "- name: Persist the converged control-plane bootstrap contract")
	if selectRoles < 0 || imported < 0 || laneEnd < 0 || persist < 0 {
		t.Fatal("converge playbook must select the requested roles, import bootstrap.yml, and end before persisting in lane mode")
	}
	if !(selectRoles < imported && imported < laneEnd && laneEnd < persist) {
		t.Fatal("roles must be selected before bootstrap.yml, and lane mode must end before the contract is persisted")
	}
	if !strings.Contains(playbook, "!= faas_cp_environment_sha256") {
		t.Fatal("an operator variable or inventory change must converge every role")
	}
}

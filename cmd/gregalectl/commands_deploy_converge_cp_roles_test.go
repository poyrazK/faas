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
	if vars["faas_stateful_restart_allowed"] != false {
		t.Errorf("the lane must never restart PostgreSQL or pgbouncer; vars = %v", vars)
	}
	if _, ok := wholeVars["faas_stateful_restart_allowed"]; ok {
		t.Error("a full release still restarts PostgreSQL when a role needs it")
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

// CD stages the seal-domain keys beside the vars file and names them by
// absolute path. Two runners (/opt/actions-runner, /opt/actions-runner-2)
// and two work directories (cp-converge, host-config) must not move the
// contract or the environment: on rc.343 and rc.344 they did, and every
// full release converged every control-plane role.
func TestControlPlaneHashesIgnoreTheVarsDirectory(t *testing.T) {
	root := t.TempDir()
	inventoryRoot := filepath.Join(root, "inventory-root")
	inventory := []manifestAnsibleFile{{Path: filepath.Join(inventoryRoot, "inventory", "hosts.ini"), Body: []byte("[control_plane]\ncp\n")}}
	ansibleDir := filepath.Join(root, "ansible")
	writeContractTree(t, ansibleDir, map[string]string{
		"bootstrap.yml":                 "- name: cp\n  hosts: control_plane\n  roles:\n    - role: postgres\n",
		"control_plane_converge.yml":    "- import_playbook: bootstrap.yml\n",
		"roles/postgres/tasks/main.yml": "- debug: {}\n",
	})
	stage := func(work, recipient string) string {
		vars := filepath.Join(work, "vars.yml")
		body := "faas_seal_domains_managed: true\n" +
			"faas_seal_domain_outbound_identity_src: \"" + filepath.Join(work, "seal-domains", "outbound.age") + "\"\n" +
			"# outbound recipient: " + recipient + "\n"
		if err := os.MkdirAll(work, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vars, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return vars
	}
	hashes := func(vars string) (string, string) {
		t.Helper()
		contract, err := controlPlaneBootstrapContractHash(ansibleDir, inventoryRoot, inventory, vars)
		if err != nil {
			t.Fatal(err)
		}
		env, err := controlPlaneEnvironmentHash(inventoryRoot, inventory, vars)
		if err != nil {
			t.Fatal(err)
		}
		return contract, env
	}
	c1, e1 := hashes(stage(filepath.Join(root, "actions-runner", "_work", "_temp", "cp-converge"), "age1aaa"))
	c2, e2 := hashes(stage(filepath.Join(root, "actions-runner-2", "_work", "_temp", "host-config"), "age1aaa"))
	if c1 != c2 {
		t.Errorf("contract depends on the vars directory: %s != %s", c1, c2)
	}
	if e1 != e2 {
		t.Errorf("environment depends on the vars directory: %s != %s", e1, e2)
	}
	// A rotated key still reaches both hashes through its recipient.
	c3, e3 := hashes(stage(filepath.Join(root, "actions-runner", "_work", "_temp", "cp-converge"), "age1bbb"))
	if c3 == c1 || e3 == e1 {
		t.Errorf("a rotated recipient must change the contract and the environment")
	}
	// A path outside the vars directory is content, and stays in the hash.
	vars := stage(filepath.Join(root, "w"), "age1aaa")
	if err := os.WriteFile(vars, []byte("x_src: \"/etc/faas/other.age\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ea := hashes(vars)
	if err := os.WriteFile(vars, []byte("x_src: \"/etc/faas/moved.age\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, eb := hashes(vars); ea == eb {
		t.Errorf("a path outside the vars directory must stay an input")
	}
}

// Every role entry carries its lane's selection condition, so a --roles
// convergence skips exactly the roles it did not name: the control-plane
// play faas_cp_converge_roles (ADR-20261030150000), the compute play, which
// node_join imports, faas_join_converge_roles (ADR-20261115120000). Neither
// carries the other's.
func TestBootstrapRolesCarryTheirLaneSelection(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []map[string]any
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	lanes := map[string]string{"control_plane": "faas_cp_converge_roles", "compute_nodes": "faas_join_converge_roles"}
	saw := map[string]bool{}
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
			for group, variable := range lanes {
				selection := regexp.MustCompile(`^` + variable + ` \| default\(none\) is none or '([a-z0-9_]+)' in ` + variable + `$`)
				var selected []string
				for _, c := range conditions {
					if m := selection.FindStringSubmatch(c); m != nil {
						selected = append(selected, m[1])
					} else if strings.Contains(c, variable) {
						t.Errorf("%s: role %s has a malformed selection condition %q", hosts, name, c)
					}
				}
				if hosts == group {
					saw[group] = true
					if len(selected) != 1 || selected[0] != name {
						t.Errorf("%s role %s must carry its own %s condition; conditions = %q", group, name, variable, conditions)
					}
				} else if len(selected) != 0 {
					t.Errorf("%s role %s must not carry the %s selection", hosts, name, variable)
				}
			}
		}
	}
	for group := range lanes {
		if !saw[group] {
			t.Fatalf("bootstrap.yml has no %s play with roles", group)
		}
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
	guard := strings.Index(playbook, "- name: Require the stateful restart guard in lane mode")
	if guard < 0 || guard > imported || !strings.Contains(playbook, "- not (faas_stateful_restart_allowed | default(true) | bool)") {
		t.Fatal("lane mode must refuse to run without the stateful restart guard, before bootstrap.yml")
	}
}

// adr: 20261030150000

// A full release converges the requested roles as the whole play would:
// PostgreSQL and pgbouncer may restart, and the playbook records the
// contract. Only the role selection and the environment check are the
// lane's.
func TestConvergeControlPlaneFullReleaseRolesMode(t *testing.T) {
	opts := convergeFixture(t)
	opts.Roles = []string{"grafana"}
	opts.FullRelease = true
	report, vars := convergeVarsOf(t, opts)
	if got := strings.Join(report.Roles, ","); got != "grafana" {
		t.Errorf("report roles = %q", got)
	}
	if requested, _ := vars["faas_cp_converge_roles_requested"].([]any); len(requested) != 1 || requested[0] != "grafana" {
		t.Errorf("faas_cp_converge_roles_requested = %v", vars["faas_cp_converge_roles_requested"])
	}
	if vars["faas_cp_environment_sha256"] != report.Environment || vars["faas_cp_full_release"] != true {
		t.Errorf("a full release's --roles must pass the environment and faas_cp_full_release; vars = %v", vars)
	}
	for _, key := range []string{"faas_cp_record_contract", "faas_stateful_restart_allowed"} {
		if _, ok := vars[key]; ok {
			t.Errorf("a full release must not pass %s: it records the contract and may restart PostgreSQL", key)
		}
	}
	if vars["faas_join_defer_service_handlers"] != true {
		t.Errorf("activation restarts the FaaS daemons; vars = %v", vars)
	}
}

// With --json, callers redirect stdout to a file and parse it, so the
// play's own output must go to stderr.
func TestCmdDeployConvergeControlPlaneJSONKeepsPlayOutputOffStdout(t *testing.T) {
	opts := convergeFixture(t)
	ansiblePlaybookRunner = func(context.Context, string, []string) error {
		_, err := os.Stdout.WriteString("PLAY RECAP *****\nfsn-1 : ok=3 changed=1\n")
		return err
	}
	var code int
	var stderr string
	stdout, _ := captureStdoutComputeNodes(t, func() {
		stderr = captureStderrComputeNodes(t, func() {
			code = cmdDeployDispatch([]string{"converge-control-plane", "--manifest-file", opts.ManifestFile,
				"--ssh-known-hosts-file", opts.SSHKnownHostsFile, "--ansible-vars-file", opts.AnsibleVarsFile,
				"--repo-root", opts.RepoRoot, "--roles", "grafana", "--full-release", "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit code = %d; stderr %q", code, stderr)
	}
	var report deployConvergeControlPlaneReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not only the JSON report: %v\n%s", err, stdout)
	}
	if !strings.HasPrefix(report.Contract, "sha256:") || !strings.HasPrefix(report.Environment, "sha256:") {
		t.Errorf("report = %+v", report)
	}
	if !strings.Contains(stderr, "PLAY RECAP") {
		t.Errorf("the play's output must reach stderr; stderr = %q", stderr)
	}
}

// Requested roles were picked by content hash, which covers inputs the
// contract misses, so they run even when the contract is current; the
// stateful guard binds the lane, not a full release.
func TestControlPlaneConvergePlaybookRolesSkipTheContractFastPath(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", controlPlaneConvergePlaybook))
	if err != nil {
		t.Fatal(err)
	}
	var plays []struct {
		Tasks []map[string]any `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	var fastPath string
	var guardWhen []any
	for _, task := range plays[0].Tasks {
		switch task["name"] {
		case "Select convergence or the unchanged fast path":
			facts, _ := task["ansible.builtin.set_fact"].(map[string]any)
			fastPath, _ = facts["faas_join_bootstrap_contract_current"].(string)
		case "Require the stateful restart guard in lane mode":
			guardWhen, _ = task["when"].([]any)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(fastPath), "{{ faas_cp_converge_roles_requested is not defined") {
		t.Errorf("requested roles must never take the contract fast path: %q", fastPath)
	}
	if len(guardWhen) != 2 || guardWhen[0] != "faas_cp_converge_roles_requested is defined" ||
		guardWhen[1] != "not (faas_cp_full_release | default(false) | bool)" {
		t.Errorf("the stateful guard must bind every --roles run except a full release's: when = %v", guardWhen)
	}
}

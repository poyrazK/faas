// adr: 20261023104734 — daemons log in as their own faas_runtime member role.
package daemonunitspec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runtimeRolesSQL is the script the postgres role runs to create the roles.
func runtimeRolesSQL(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "postgres", "files", "runtime-roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// peerRuntimeRoles returns the os_user -> role lines of faas_map that name a
// runtime role, and the per-daemon roles runtime-roles.sql creates.
func peerRuntimeRoles(t *testing.T) (map[string]string, map[string]bool) {
	t.Helper()
	_, byName := roleTasksByName(t, "postgres")
	ident, _ := byName["postgres — map control-plane service users to database role faas"].body["ansible.builtin.blockinfile"].(map[string]any)
	block, _ := ident["block"].(string)
	if block == "" {
		t.Fatal("postgres role has no faas_map ident block")
	}
	mapped := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^faas_map\s+(\S+)\s+(faas_\S+)\s*$`).FindAllStringSubmatch(block, -1) {
		if prev, ok := mapped[m[1]]; ok {
			t.Errorf("faas_map maps %s to two runtime roles: %s and %s", m[1], prev, m[2])
		}
		mapped[m[1]] = m[2]
	}
	array := regexp.MustCompile(`(?s)FOREACH r IN ARRAY ARRAY\[(.*?)\] LOOP`).FindStringSubmatch(runtimeRolesSQL(t))
	if array == nil {
		t.Fatal("runtime-roles.sql has no per-daemon role array")
	}
	created := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(array[1], -1) {
		created[m[1]] = true
	}
	return mapped, created
}

// Every OS user the ident map trusts as the owner also gets a runtime role,
// and every peer role the script creates is reachable through exactly one
// daemon user. Otherwise a unit either cannot opt in or a role is orphaned.
func TestRuntimeRolesCoverEveryMappedDaemonUser(t *testing.T) {
	mapped, created := peerRuntimeRoles(t)
	_, byName := roleTasksByName(t, "postgres")
	ident, _ := byName["postgres — map control-plane service users to database role faas"].body["ansible.builtin.blockinfile"].(map[string]any)
	for _, m := range regexp.MustCompile(`(?m)^faas_map\s+(\S+)\s+faas\s*$`).FindAllStringSubmatch(ident["block"].(string), -1) {
		if m[1] == "root" {
			continue // root runs migrations and operator tools as the owner.
		}
		if _, ok := mapped[m[1]]; !ok {
			t.Errorf("%s logs in as the owner but has no runtime role in faas_map", m[1])
		}
	}
	seen := map[string]bool{}
	for user, role := range mapped {
		if !created[role] {
			t.Errorf("faas_map maps %s to %s, which runtime-roles.sql does not create", user, role)
		}
		if seen[role] {
			t.Errorf("runtime role %s is shared by two daemon users", role)
		}
		seen[role] = true
	}
	for role := range created {
		if !seen[role] {
			t.Errorf("runtime-roles.sql creates %s but faas_map maps no daemon user to it", role)
		}
	}
	compute := regexp.MustCompile(`(?s)FOREACH r IN ARRAY ARRAY\[('faas_compute'.*?)\] LOOP(.*?)END LOOP`).FindStringSubmatch(runtimeRolesSQL(t))
	if compute == nil || !strings.Contains(compute[2], "GRANT faas_runtime TO %I") {
		t.Error("runtime-roles.sql does not add faas_compute (and the per-daemon compute roles) to faas_runtime")
	}
}

// The control-plane opt-in may only name a unit's real User= and a role that
// user is mapped to, or the peer-login probe and the daemon both fail.
func TestRuntimeRoleUnitsMatchTheirUsers(t *testing.T) {
	mapped, _ := peerRuntimeRoles(t)
	defaults := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "control_plane_service", "defaults", "main.yml")
	body, err := os.ReadFile(defaults)
	if err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Enabled bool `yaml:"faas_postgres_runtime_roles_enabled"`
		Units   []struct {
			Unit    string `yaml:"unit"`
			OSUser  string `yaml:"os_user"`
			Role    string `yaml:"role"`
			Restart string `yaml:"restart"`
		} `yaml:"faas_postgres_runtime_role_units"`
	}
	if err := yaml.Unmarshal(body, &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Enabled {
		t.Error("runtime roles must stay opt-in until the contract step")
	}
	if len(vars.Units) == 0 {
		t.Fatal("faas_postgres_runtime_role_units is empty")
	}
	handlers, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "control_plane_service", "handlers", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	userLine := regexp.MustCompile(`(?m)^User=(\S+)\s*$`)
	for _, u := range vars.Units {
		unit, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "control_plane_service", "files", u.Unit+".service"))
		if err != nil {
			t.Errorf("%s: %v", u.Unit, err)
			continue
		}
		if m := userLine.FindSubmatch(unit); m == nil || string(m[1]) != u.OSUser {
			t.Errorf("%s runs as %q, but the opt-in names %s", u.Unit, m, u.OSUser)
		}
		if mapped[u.OSUser] != u.Role {
			t.Errorf("%s: faas_map maps %s to %q, not %s", u.Unit, u.OSUser, mapped[u.OSUser], u.Role)
		}
		if !strings.Contains(string(handlers), "listen: "+u.Restart+"\n") {
			t.Errorf("%s: no handler listens on %q", u.Unit, u.Restart)
		}
	}
}

// renderJinja renders an Ansible task's inline Jinja content with vars (a
// JSON object), the way Ansible templates a module argument, including
// Ansible's bool filter.
func renderJinja(t *testing.T, content, vars string) string {
	t.Helper()
	script := `import json, sys
from jinja2 import Environment
env = Environment()
env.filters["bool"] = lambda v: v if isinstance(v, bool) else str(v).strip().lower() in ("yes", "on", "1", "true")
print(env.from_string(sys.argv[1]).render(**json.loads(sys.argv[2])), end="")
`
	out, err := exec.Command("python3", "-c", script, content, vars).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v: %s", err, out)
	}
	return string(out)
}

func requireJinja(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for template rendering")
	}
}

// Both roles that rewrite pg_hba.conf must admit the runtime group before the
// reject lines, or a node join silently locks every opted-in daemon out. Once
// the owner is contracted (step 2) neither may let a password log in as it.
func TestPgHbaAdmitsTheRuntimeGroup(t *testing.T) {
	requireJinja(t)
	ownerHost := regexp.MustCompile(`(?m)^host\s+faas\s+faas\s`)
	for role, taskName := range map[string]string{
		"postgres":                  "postgres — narrow pg_hba.conf to peer/local auth (spec §11)",
		"control_plane_peer_access": "control-plane peer access — render manifest-derived pg_hba entries",
	} {
		_, byName := roleTasksByName(t, role)
		copyArgs, _ := byName[taskName].body["ansible.builtin.copy"].(map[string]any)
		content, _ := copyArgs["content"].(string)
		for _, contracted := range []bool{false, true} {
			vars := fmt.Sprintf(`{"faas_postgres_allowed_cidrs": ["10.42.0.2/32"], "faas_postgres_owner_contracted": %t, "control_plane_postgres_owner_contracted": {"stat": {"exists": %t}}}`, contracted, contracted)
			hba := renderJinja(t, content, vars)
			reject := strings.Index(hba, "host    all             all             127.0.0.1/32            reject")
			for _, want := range []string{
				"local   all             faas                                    peer map=faas_map",
				"local   faas            +faas_runtime                           peer map=faas_map",
				"host    faas            +faas_runtime                           127.0.0.1/32            scram-sha-256",
				"host    faas            +faas_runtime                           10.42.0.2/32                scram-sha-256",
			} {
				if line := strings.Index(hba, want); line < 0 || reject < line {
					t.Errorf("%s (contracted=%t): pg_hba missing %q before the reject lines:\n%s", role, contracted, want, hba)
				}
			}
			if got := ownerHost.MatchString(hba); got == contracted {
				t.Errorf("%s (contracted=%t): owner password lines present=%t:\n%s", role, contracted, got, hba)
			}
		}
	}
}

// After the contract only root maps to the owner, and every daemon user keeps
// its runtime role, so the converge that removes the owner lines leaves each
// opted-in daemon able to log in. The live check runs before anything changes.
func TestOwnerContractLeavesOnlyRoot(t *testing.T) {
	requireJinja(t)
	tasks, byName := roleTasksByName(t, "postgres")
	ident, _ := byName["postgres — map control-plane service users to database role faas"].body["ansible.builtin.blockinfile"].(map[string]any)
	block, _ := ident["block"].(string)
	owner := regexp.MustCompile(`(?m)^faas_map\s+(\S+)\s+faas\s*$`)
	runtime, _ := peerRuntimeRoles(t)
	for _, contracted := range []bool{false, true} {
		rendered := renderJinja(t, block, fmt.Sprintf(`{"faas_postgres_owner_contracted": %t}`, contracted))
		var users []string
		for _, m := range owner.FindAllStringSubmatch(rendered, -1) {
			users = append(users, m[1])
		}
		if contracted && strings.Join(users, ",") != "root" {
			t.Errorf("contracted faas_map maps %v to the owner, want only root", users)
		}
		if !contracted && len(users) != len(runtime)+1 {
			t.Errorf("faas_map maps %v to the owner before the contract, want root and the %d daemon users", users, len(runtime))
		}
		for user, role := range runtime {
			if !regexp.MustCompile(`(?m)^faas_map\s+` + regexp.QuoteMeta(user) + `\s+` + role + `\s*$`).MatchString(rendered) {
				t.Errorf("contracted=%t: faas_map lost %s -> %s", contracted, user, role)
			}
		}
	}

	position := map[string]int{}
	for i, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			position[name] = i
		}
	}
	refuse := "postgres — refuse to contract the owner while something still uses it — ADR-20261023104734"
	if _, ok := position[refuse]; !ok {
		t.Fatalf("postgres role has no task %q", refuse)
	}
	for _, later := range []string{
		"postgres — narrow pg_hba.conf to peer/local auth (spec §11)",
		"postgres — map control-plane service users to database role faas",
		"postgres — record the owner contract for node joins — ADR-20261023104734",
	} {
		if position[refuse] >= position[later] {
			t.Errorf("%q runs before the contract's live check", later)
		}
	}
	stray := byName["postgres — remove owner mappings outside the managed block when contracted — ADR-20261023104734"]
	strayArgs, _ := stray.body["ansible.builtin.lineinfile"].(map[string]any)
	if strayArgs["state"] != "absent" || !strings.Contains(fmt.Sprint(strayArgs["regexp"]), "(?!root\\s)") ||
		!strings.Contains(whenClauses(stray), "faas_postgres_owner_contracted") {
		t.Errorf("the contract no longer removes owner mappings written outside the managed block (keeping root's): %v", stray.body)
	}
	probe, _ := byName["postgres — read the logins that still use the owner — ADR-20261023104734"].body["ansible.builtin.command"].(map[string]any)
	if !strings.Contains(fmt.Sprint(probe["argv"]), "usename = 'faas'") {
		t.Error("the contract's live check no longer looks for sessions logged in as the owner")
	}
}

// postgres_shared_runtime_role moves the units that run as the shared `faas`
// OS user. Each must really run as faas (faas_map faas faas_shared is the
// only line its peer login can use), and the role must run after the role
// that installs the unit, or a first converge finds nothing to move.
func TestSharedRuntimeRoleUnitsRunAsFaas(t *testing.T) {
	root := repoRoot(t)
	mapped, _ := peerRuntimeRoles(t)
	var vars struct {
		Role   string   `yaml:"faas_postgres_shared_runtime_role"`
		OSUser string   `yaml:"faas_postgres_shared_runtime_role_os_user"`
		Units  []string `yaml:"faas_postgres_shared_runtime_role_units"`
	}
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "postgres_shared_runtime_role", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, &vars); err != nil {
		t.Fatal(err)
	}
	if vars.OSUser != "faas" || mapped[vars.OSUser] != vars.Role {
		t.Fatalf("shared runtime role %s for OS user %s, but faas_map maps it to %q", vars.Role, vars.OSUser, mapped[vars.OSUser])
	}
	playbook, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	moveAt := strings.Index(string(playbook), "- role: postgres_shared_runtime_role\n")
	if moveAt < 0 {
		t.Fatal("bootstrap.yml does not run postgres_shared_runtime_role")
	}
	userLine := regexp.MustCompile(`(?m)^User=(\S+)\s*$`)
	for _, unit := range vars.Units {
		files, _ := filepath.Glob(filepath.Join(root, "deploy", "ansible", "roles", "*", "files", unit+".service"))
		if len(files) != 1 {
			t.Errorf("%s: want one role installing it, found %v", unit, files)
			continue
		}
		unitBody, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if m := userLine.FindSubmatch(unitBody); m == nil || string(m[1]) != vars.OSUser {
			t.Errorf("%s runs as %q, not the shared %s user", unit, m, vars.OSUser)
		}
		owner := filepath.Base(filepath.Dir(filepath.Dir(files[0])))
		if at := strings.Index(string(playbook), "- role: "+owner+"\n"); at < 0 || at > moveAt {
			t.Errorf("%s: its role %s does not run before postgres_shared_runtime_role in bootstrap.yml", unit, owner)
		}
	}
}

// The grant script and the compute role must skip a read-only standby, which
// receives roles and grants from the primary.
func TestRuntimeRoleTasksSkipTheStandby(t *testing.T) {
	_, byName := roleTasksByName(t, "postgres")
	for _, name := range []string{
		"postgres — require a strong compute runtime password when set — ADR-20261023104734",
		"postgres — create compute runtime role `faas_compute` — ADR-20261023104734",
		"postgres — converge runtime roles and grants — ADR-20261023104734",
	} {
		task, ok := byName[name]
		if !ok {
			t.Errorf("postgres role has no task %q", name)
			continue
		}
		if !strings.Contains(whenClauses(task), "not (faas_postgres_standby_mode | default(false) | bool)") {
			t.Errorf("%q runs on a standby", name)
		}
	}
	if byName["postgres — create compute runtime role `faas_compute` — ADR-20261023104734"].body["no_log"] != true {
		t.Error("the faas_compute task logs the compute password")
	}
}

// The audit log and its anchors (ADR-20261026163015) are append-only for
// daemons: runtime-roles.sql revokes row changes, so no query a daemon runs
// may update or delete them. A new one would fail with permission denied
// once units move to their runtime roles.
func TestAuditLogStaysAppendOnly(t *testing.T) {
	for _, table := range []string{"audit_log", "audit_log_anchors"} {
		if !strings.Contains(runtimeRolesSQL(t), "REVOKE UPDATE, DELETE ON public."+table+" FROM faas_runtime;") {
			t.Errorf("runtime-roles.sql no longer makes %s append-only for faas_runtime", table)
		}
	}
	mutation := regexp.MustCompile(`(?i)\b(UPDATE\s+(public\.)?audit_log(_anchors)?\b|DELETE\s+FROM\s+(public\.)?audit_log(_anchors)?\b|TRUNCATE\s+(TABLE\s+)?(public\.)?audit_log(_anchors)?\b)`)
	root := repoRoot(t)
	for _, dir := range []string{"pkg", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if !strings.HasSuffix(path, ".sql") && !strings.HasSuffix(path, ".go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if m := mutation.Find(body); m != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s: %q changes audit log rows, which daemons may not", rel, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

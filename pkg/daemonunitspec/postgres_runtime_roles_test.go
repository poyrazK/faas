// adr: 20261023104734 — daemons log in as their own faas_runtime member role.
package daemonunitspec

import (
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
	if !strings.Contains(runtimeRolesSQL(t), "GRANT faas_runtime TO faas_compute") {
		t.Error("runtime-roles.sql does not add faas_compute to faas_runtime")
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

// Both roles that rewrite pg_hba.conf must admit the runtime group before the
// reject lines, or a node join silently locks every opted-in daemon out.
func TestPgHbaAdmitsTheRuntimeGroup(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for pg_hba rendering")
	}
	script := `import sys
from jinja2 import Template
print(Template(sys.argv[1]).render(faas_postgres_allowed_cidrs=["10.42.0.2/32"]), end="")
`
	for role, taskName := range map[string]string{
		"postgres":                  "postgres — narrow pg_hba.conf to peer/local auth (spec §11)",
		"control_plane_peer_access": "control-plane peer access — render manifest-derived pg_hba entries",
	} {
		_, byName := roleTasksByName(t, role)
		copyArgs, _ := byName[taskName].body["ansible.builtin.copy"].(map[string]any)
		content, _ := copyArgs["content"].(string)
		out, err := exec.Command("python3", "-c", script, content).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: render pg_hba: %v: %s", role, err, out)
		}
		hba := string(out)
		reject := strings.Index(hba, "host    all             all             127.0.0.1/32            reject")
		for _, want := range []string{
			"local   faas            +faas_runtime                           peer map=faas_map",
			"host    faas            +faas_runtime                           127.0.0.1/32            scram-sha-256",
			"host    faas            +faas_runtime                           10.42.0.2/32                scram-sha-256",
		} {
			if line := strings.Index(hba, want); line < 0 || reject < line {
				t.Errorf("%s: pg_hba missing %q before the reject lines:\n%s", role, want, hba)
			}
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

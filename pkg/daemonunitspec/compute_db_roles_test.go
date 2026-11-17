// adr: 20261023104734 — step 3: every compute daemon logs in as its own role.
package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// computeUnitRoles reads scripts/ci/compute_db_roles.py's UNIT_ROLES table,
// the single place the unit -> role pairs are written down.
func computeUnitRoles(t *testing.T) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "ci", "compute_db_roles.py"))
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile(`(?s)UNIT_ROLES = \{(.*?)\}`).FindSubmatch(body)
	if table == nil {
		t.Fatal("compute_db_roles.py has no UNIT_ROLES table")
	}
	pairs := map[string]string{}
	for _, m := range regexp.MustCompile(`"([a-z-]+)": "([a-z_]+)"`).FindAllSubmatch(table[1], -1) {
		pairs[string(m[1])] = string(m[2])
	}
	if len(pairs) == 0 {
		t.Fatal("UNIT_ROLES is empty")
	}
	return pairs
}

// The derivation script, node_join's unit list and runtime-roles.sql must
// name the same daemons and roles: a unit node_join moves without a role the
// control plane creates cannot log in, and a role runtime-roles.sql does not
// add to faas_runtime can log in but read nothing.
func TestComputeDaemonRolesAgreeAcrossCDAndAnsible(t *testing.T) {
	pairs := computeUnitRoles(t)
	units := make([]string, 0, len(pairs))
	roles := map[string]bool{}
	for unit, role := range pairs {
		units = append(units, unit)
		if !regexp.MustCompile(`^faas_[a-z][a-z0-9_]{0,40}$`).MatchString(role) {
			t.Errorf("%s: role %q is not a faas_<daemon> runtime role", unit, role)
		}
		if roles[role] {
			t.Errorf("role %s is shared by two compute units", role)
		}
		roles[role] = true
	}
	sort.Strings(units)

	var join []struct {
		Vars struct {
			Units []string `yaml:"faas_join_db_role_units"`
		} `yaml:"vars"`
	}
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, &join); err != nil {
		t.Fatal(err)
	}
	var joinUnits []string
	for _, play := range join {
		joinUnits = append(joinUnits, play.Vars.Units...)
	}
	sort.Strings(joinUnits)
	if strings.Join(joinUnits, ",") != strings.Join(units, ",") {
		t.Errorf("node_join moves %v, but compute_db_roles.py derives logins for %v", joinUnits, units)
	}

	sql := runtimeRolesSQL(t)
	for role := range roles {
		if !strings.Contains(sql, "'"+role+"'") {
			t.Errorf("runtime-roles.sql does not add %s to faas_runtime", role)
		}
	}
	// Peer-mapped roles have no password; these log in over TCP only.
	mapped, _ := peerRuntimeRoles(t)
	for user, role := range mapped {
		if roles[role] {
			t.Errorf("compute role %s is also peer-mapped from %s", role, user)
		}
	}
}

// Every unit node_join points at a per-daemon login must read compute-db.env
// first, so the drop-in's EnvironmentFile (read after it) is what wins, and
// the role must only ever be created from a SCRAM verifier, never a password.
func TestComputeDaemonLoginsOverrideTheSharedDSN(t *testing.T) {
	root := repoRoot(t)
	for unit := range computeUnitRoles(t) {
		files, _ := filepath.Glob(filepath.Join(root, "deploy", "ansible", "roles", "*", "files", unit+".service"))
		if len(files) == 0 {
			t.Errorf("%s: no role installs this unit", unit)
		}
		for _, f := range files {
			body, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "EnvironmentFile=-/etc/faas/compute-db.env") {
				t.Errorf("%s does not read compute-db.env; the per-daemon login would have nothing to override", f)
			}
		}
	}
	_, byName := roleTasksByName(t, "postgres")
	task, ok := byName["postgres — create per-daemon compute roles — ADR-20261023104734"]
	if !ok {
		t.Fatal("postgres role does not create the per-daemon compute roles")
	}
	when := whenClauses(task)
	if !strings.Contains(when, `SCRAM-SHA-256`) || !strings.Contains(when, "faas_postgres_standby_mode") || task.body["no_log"] != true {
		t.Errorf("per-daemon compute roles must take only SCRAM verifiers, skip the standby and never log: %v", task.body)
	}
}

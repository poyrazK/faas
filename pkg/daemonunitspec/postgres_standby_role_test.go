// adr: 1344 — warm-standby replica (§8): primary-side settings, the standby
// role's wipe guard, the playbook and the firewall.
package daemonunitspec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func roleTasksByName(t *testing.T, role string) ([]roleTask, map[string]roleTask) {
	t.Helper()
	tasks := flattenRoleTasks("main.yml", loadYAMLList(t, filepath.Join(repoRoot(t), "deploy", "ansible", "roles", role, "tasks", "main.yml")))
	byName := map[string]roleTask{}
	for _, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			byName[name] = task
		}
	}
	return tasks, byName
}

func whenClauses(task roleTask) string {
	switch when := task.body["when"].(type) {
	case string:
		return when
	case []any:
		var parts []string
		for _, w := range when {
			parts = append(parts, w.(string))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// A standby is read-only: every task of the postgres role that writes to the
// database must skip it, or the standby converge fails (or, before seeding,
// creates the faas database the wipe guard treats as a primary's).
func TestPostgresRoleSkipsDatabaseWritesOnStandby(t *testing.T) {
	_, byName := roleTasksByName(t, "postgres")
	for _, name := range []string{
		"postgres — create postgres role `faas`",
		"postgres — create database `faas`",
		"postgres — require a password for split-box database clients",
		"postgres — create replication role `replicator` — ADR-1344",
		"postgres — create the standby's physical replication slot — ADR-1344",
		"postgres — drop the abandoned standby slot — ADR-1344",
		"postgres — remove standby settings from a primary — ADR-1344",
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
	for _, name := range []string{
		"postgres — create replication role `replicator` — ADR-1344",
		"postgres — create the standby's physical replication slot — ADR-1344",
		"postgres — cap WAL kept for the standby slot (reload-only) — ADR-1344",
	} {
		if !strings.Contains(whenClauses(byName[name]), "faas_postgres_standby_address | default('')") {
			t.Errorf("%q is not keyed off a declared standby", name)
		}
	}
	if task := byName["postgres — create replication role `replicator` — ADR-1344"]; task.body["no_log"] != true {
		t.Error("the replicator task logs the replication password")
	}
	drop, _ := byName["postgres — drop the abandoned standby slot — ADR-1344"].body["ansible.builtin.command"].(map[string]any)
	var sql []string
	for _, arg := range drop["argv"].([]any) {
		sql = append(sql, arg.(string))
	}
	joined := strings.Join(sql, "\n")
	nologin, terminate, dropSlot := strings.Index(joined, "NOLOGIN"), strings.Index(joined, "pg_terminate_backend"), strings.Index(joined, "pg_drop_replication_slot")
	if nologin < 0 || terminate < nologin || dropSlot < terminate {
		t.Errorf("abandoned slot drop must close the replicator login, end the walsender, then drop: %v", sql)
	}
}

// Both roles that rewrite pg_hba.conf must carry the replication line, or a
// node join silently cuts the standby off.
func TestPgHbaAdmitsTheStandbyOnlyWhenDeclared(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for pg_hba rendering")
	}
	for role, taskName := range map[string]string{
		"postgres":                  "postgres — narrow pg_hba.conf to peer/local auth (spec §11)",
		"control_plane_peer_access": "control-plane peer access — render manifest-derived pg_hba entries",
	} {
		_, byName := roleTasksByName(t, role)
		copyArgs, _ := byName[taskName].body["ansible.builtin.copy"].(map[string]any)
		content, _ := copyArgs["content"].(string)
		if content == "" {
			t.Fatalf("%s: no pg_hba content in %q", role, taskName)
		}
		script := `import sys
from jinja2 import Template
kw = {"faas_postgres_allowed_cidrs": ["10.42.0.2/32"]}
if sys.argv[2]:
    kw["faas_postgres_standby_address"] = sys.argv[2]
print(Template(sys.argv[1]).render(**kw), end="")
`
		for _, tc := range []struct{ address, want string }{
			{"10.42.0.9", "hostssl replication     replicator      10.42.0.9/32"},
			{"fd00::9", "hostssl replication     replicator      fd00::9/128"},
			{"", ""},
		} {
			out, err := exec.Command("python3", "-c", script, content, tc.address).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: render pg_hba: %v: %s", role, err, out)
			}
			hba := string(out)
			if tc.want == "" {
				if strings.Contains(hba, "replicator") {
					t.Errorf("%s: pg_hba admits replication without a standby:\n%s", role, hba)
				}
				continue
			}
			line := strings.Index(hba, tc.want)
			reject := strings.Index(hba, "host    all             all             127.0.0.1/32            reject")
			if line < 0 || reject < line {
				t.Errorf("%s: pg_hba missing %q before the reject lines:\n%s", role, tc.want, hba)
			}
		}
	}
}

// Seeding deletes the standby's data directory. The guard must run before
// the seed block, and the delete must be preceded by the data-directory check.
func TestPostgresStandbySeedIsGuarded(t *testing.T) {
	tasks, byName := roleTasksByName(t, "postgres_standby")
	index := map[string]int{}
	for i, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			index[name] = i
		}
	}
	guard := "postgres_standby — refuse to wipe a cluster that may be a primary"
	seed := "postgres_standby — seed the replica from the primary"
	if _, ok := index[guard]; !ok || index[guard] > index[seed] {
		t.Fatalf("the wipe guard must run before %q", seed)
	}
	assertArgs, _ := byName[guard].body["ansible.builtin.assert"].(map[string]any)
	that := assertArgs["that"].([]any)[0].(string)
	if !strings.Contains(that, "postgres_standby_safe_to_wipe") {
		t.Errorf("wipe guard does not check postgres_standby_safe_to_wipe: %s", that)
	}
	decide, _ := byName["postgres_standby — decide whether to seed"].body["ansible.builtin.set_fact"].(map[string]any)
	safe, _ := decide["postgres_standby_safe_to_wipe"].(string)
	for _, want := range []string{"matched == 0", "postgres_standby_local_state.rc == 0", "!= '0:1'", "faas_postgres_standby_wipe_primary == inventory_hostname"} {
		if !strings.Contains(safe, want) {
			t.Errorf("safe-to-wipe evidence missing %q: %s", want, safe)
		}
	}
	if !strings.Contains(whenClauses(byName[seed]), "postgres_standby_needs_seed") {
		t.Error("the seed block is not gated on postgres_standby_needs_seed")
	}
	check, wipe, copyData := index["postgres_standby — require a PostgreSQL data directory at the wipe path"], index["postgres_standby — empty the local data directory"], index["postgres_standby — copy the primary with pg_basebackup"]
	if check == 0 || wipe < check || copyData < wipe {
		t.Error("seed must check the data directory, then empty it, then copy")
	}
	basebackup, _ := byName["postgres_standby — copy the primary with pg_basebackup"].body["ansible.builtin.command"].(map[string]any)
	var argv []string
	for _, arg := range basebackup["argv"].([]any) {
		argv = append(argv, arg.(string))
	}
	for _, want := range []string{"--slot={{ faas_postgres_standby_slot }}", "--wal-method=stream", "--no-password"} {
		if !strings.Contains(strings.Join(argv, " "), want) {
			t.Errorf("pg_basebackup missing %s: %v", want, argv)
		}
	}
	for _, name := range []string{"postgres_standby — copy the primary with pg_basebackup", "postgres_standby — confirm the replicator can reach the primary"} {
		env, _ := byName[name].body["environment"].(map[string]any)
		if env["PGPASSFILE"] != "/var/lib/postgresql/.pgpass" {
			t.Errorf("%q does not read the replication password file", name)
		}
	}
	if byName["postgres_standby — install the replication password file"].body["no_log"] != true {
		t.Error("the .pgpass task logs the replication password")
	}
}

// The standby settings mirror the primary's hot-standby limits, and the
// postgres role removes them once the host is a primary.
func TestPostgresStandbySettingsFile(t *testing.T) {
	root := repoRoot(t)
	template, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "postgres_standby", "templates", "90-faas-standby.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"primary_conninfo", "passfile=/var/lib/postgresql/.pgpass", "sslmode=verify-full sslrootcert={{ faas_postgres_standby_primary_cert }}", "primary_slot_name", "hot_standby = on", "faas_postgres_standby_mirrored_settings"} {
		if !strings.Contains(string(template), want) {
			t.Errorf("standby settings template missing %q", want)
		}
	}
	if strings.Contains(string(template), "password=") {
		t.Error("standby settings template puts a password in primary_conninfo")
	}
	defaults, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "postgres_standby", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"max_connections", "max_prepared_transactions", "max_locks_per_transaction", "max_wal_senders", "max_worker_processes"} {
		if !strings.Contains(string(defaults), "  - "+setting+"\n") {
			t.Errorf("standby does not mirror %s from the primary", setting)
		}
	}
	_, byName := roleTasksByName(t, "postgres")
	remove, _ := byName["postgres — remove standby settings from a primary — ADR-1344"].body["ansible.builtin.file"].(map[string]any)
	if remove["path"] != "{{ postgres_config_dir }}/conf.d/90-faas-standby.conf" || remove["state"] != "absent" {
		t.Errorf("postgres role does not remove the standby settings on a primary: %v", remove)
	}
}

// The standby converges in its own playbook, never in bootstrap's fleet plays.
func TestPostgresStandbyPlaybook(t *testing.T) {
	root := repoRoot(t)
	plays := loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "postgres_standby.yml"))
	var standbyRoles []string
	for _, item := range plays {
		play := item.(map[string]any)
		if play["hosts"] != "postgres_standby" {
			continue
		}
		for _, r := range play["roles"].([]any) {
			standbyRoles = append(standbyRoles, r.(map[string]any)["role"].(string))
		}
	}
	want := []string{"host_hardening", "node_exporter", "postgres", "postgres_standby", "control_plane_standby"}
	if strings.Join(standbyRoles, ",") != strings.Join(want, ",") {
		t.Errorf("standby play roles = %v, want %v (node_exporter before postgres for the textfile directory)", standbyRoles, want)
	}
	bootstrap, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bootstrap), "postgres_standby") {
		t.Error("bootstrap.yml must not converge the standby")
	}
}

func TestStandbyReplicationFirewallStandardJinja(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	template := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
	script := `import pathlib, sys
from jinja2 import Template
kw = dict(public_iface="eth0", masquerade_cidr="10.100.0.0/16")
if sys.argv[2]:
    kw["faas_postgres_standby_address"] = sys.argv[2]
print(Template(pathlib.Path(sys.argv[1]).read_text()).render(**kw), end="")
`
	for _, tc := range []struct{ address, want string }{
		{"10.42.0.9", `ip saddr 10.42.0.9 tcp dport 5432 accept comment "faas postgres replication to the warm standby (ADR-1344)"`},
		{"fd00::9", `ip6 saddr fd00::9 tcp dport 5432 accept`},
		{"", ""},
	} {
		out, err := exec.Command("python3", "-c", script, template, tc.address).CombinedOutput()
		if err != nil {
			t.Fatalf("render nftables: %v: %s", err, out)
		}
		if tc.want == "" {
			if strings.Contains(string(out), "warm standby") {
				t.Error("replication rule rendered without a standby")
			}
			continue
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("nftables missing %q", tc.want)
		}
	}
}

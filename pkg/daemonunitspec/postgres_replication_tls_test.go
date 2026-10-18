// adr: 1344 — warm-standby replication over verified TLS (§8): the primary's
// certificate and hostssl line, and the standby's verify-full pin.
package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	replicationTLSDirDefault = "/etc/postgresql/faas-tls"
	standbyKeyedOff          = "faas_postgres_standby_address | default('')"
	primaryOnly              = "not (faas_postgres_standby_mode | default(false) | bool)"
)

// The primary issues and serves the replication certificate only when a
// standby is declared, and never on the standby itself.
func TestPrimaryServesReplicationTLSOnlyWithAStandby(t *testing.T) {
	_, byName := roleTasksByName(t, "postgres")
	for _, name := range []string{
		"postgres — replication TLS names and paths — ADR-1344",
		"postgres — replication TLS directory — ADR-1344",
		"postgres — read the names on the replication certificate — ADR-1344",
		"postgres — check the replication certificate is valid for 30 more days — ADR-1344",
		"postgres — issue the replication certificate — ADR-1344",
		"postgres — record the names on the replication certificate — ADR-1344",
		"postgres — restrict the replication key — ADR-1344",
		"postgres — keep the distro conf.d include (replication TLS) — ADR-1344",
		"postgres — serve the replication certificate (reload-only) — ADR-1344",
	} {
		task, ok := byName[name]
		if !ok {
			t.Errorf("postgres role has no task %q", name)
			continue
		}
		when := whenClauses(task)
		if !strings.Contains(when, primaryOnly) {
			t.Errorf("%q runs on a standby", name)
		}
		if !strings.Contains(when, standbyKeyedOff) || strings.Contains(when, "not ("+standbyKeyedOff+")") {
			t.Errorf("%q is not keyed off a declared standby: %s", name, when)
		}
	}
	stop := byName["postgres — stop serving the replication certificate without a standby (reload-only) — ADR-1344"]
	if when := whenClauses(stop); !strings.Contains(when, "not ("+standbyKeyedOff+")") || !strings.Contains(when, primaryOnly) {
		t.Errorf("the replication TLS settings are not removed without a standby: %s", when)
	}
	file, _ := stop.body["ansible.builtin.file"].(map[string]any)
	serve, _ := byName["postgres — serve the replication certificate (reload-only) — ADR-1344"].body["ansible.builtin.copy"].(map[string]any)
	if file["path"] != serve["dest"] || file["state"] != "absent" {
		t.Errorf("removal path %v does not match the settings file %v", file["path"], serve["dest"])
	}
	content, _ := serve["content"].(string)
	for _, want := range []string{"ssl = on", "ssl_cert_file = '{{ postgres_replication_tls_dir }}/server.crt'", "ssl_key_file = '{{ postgres_replication_tls_dir }}/server.key'"} {
		if !strings.Contains(content, want) {
			t.Errorf("replication TLS settings missing %q", want)
		}
	}
	for _, name := range []string{"postgres — issue the replication certificate — ADR-1344", "postgres — serve the replication certificate (reload-only) — ADR-1344", "postgres — stop serving the replication certificate without a standby (reload-only) — ADR-1344"} {
		if byName[name].body["notify"] != "reload postgresql" {
			t.Errorf("%q must reload, not restart, PostgreSQL", name)
		}
	}
	paths, _ := byName["postgres — replication TLS names and paths — ADR-1344"].body["ansible.builtin.set_fact"].(map[string]any)
	if dir, _ := paths["postgres_replication_tls_dir"].(string); !strings.Contains(dir, "default('"+replicationTLSDirDefault+"')") {
		t.Errorf("primary TLS directory %q does not default to %s", dir, replicationTLSDirDefault)
	}
}

// The new pair is written beside the live one and renamed over it, so a
// failed issue cannot leave a key that does not match its certificate.
func TestReplicationCertificateIssueIsAtomic(t *testing.T) {
	_, byName := roleTasksByName(t, "postgres")
	task := byName["postgres — issue the replication certificate — ADR-1344"]
	cmd, _ := task.body["ansible.builtin.command"].(map[string]any)
	argv, _ := cmd["argv"].([]any)
	if len(argv) < 3 || argv[0] != "sh" || argv[1] != "-ec" {
		t.Fatalf("issue task argv = %v, want sh -ec SCRIPT", argv)
	}
	script := argv[2].(string)
	for _, want := range []string{"umask 077", "-days 3650", "subjectAltName=$2", `-keyout "$1/server.key.new" -out "$1/server.crt.new"`} {
		if !strings.Contains(script, want) {
			t.Errorf("issue script missing %q:\n%s", want, script)
		}
	}
	issue, key, crt := strings.Index(script, "openssl req"), strings.Index(script, `mv "$1/server.key.new" "$1/server.key"`), strings.Index(script, `mv "$1/server.crt.new" "$1/server.crt"`)
	if issue < 0 || key < issue || crt < key {
		t.Errorf("issue script must write the new pair, then rename it over the live one:\n%s", script)
	}
	if task.body["become_user"] != "postgres" {
		t.Error("the replication key must be owned by postgres")
	}
	if when := whenClauses(task); !strings.Contains(when, "postgres_replication_tls_checkend.rc != 0") || !strings.Contains(when, "!= postgres_replication_tls_san") {
		t.Errorf("certificate is not reissued on expiry or a name change: %s", when)
	}
}

// Neither role that writes pg_hba.conf may admit the replicator without TLS.
func TestPgHbaAdmitsTheReplicatorOverTLSOnly(t *testing.T) {
	for role, taskName := range map[string]string{
		"postgres":                  "postgres — narrow pg_hba.conf to peer/local auth (spec §11)",
		"control_plane_peer_access": "control-plane peer access — render manifest-derived pg_hba entries",
	} {
		_, byName := roleTasksByName(t, role)
		copyArgs, _ := byName[taskName].body["ansible.builtin.copy"].(map[string]any)
		content, _ := copyArgs["content"].(string)
		for _, line := range strings.Split(content, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[1] == "replication" && fields[2] == "replicator" && fields[0] != "hostssl" {
				t.Errorf("%s: pg_hba admits the replicator without TLS: %q", role, line)
			}
		}
	}
}

// The standby pins the primary's certificate before anything connects, and
// every connection to the primary verifies it.
func TestStandbyVerifiesThePrimary(t *testing.T) {
	tasks, byName := roleTasksByName(t, "postgres_standby")
	index := map[string]int{}
	for i, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			index[name] = i
		}
	}
	read := byName["postgres_standby — read the primary's replication certificate"]
	if read.body["delegate_to"] != "{{ faas_postgres_primary_inventory_host }}" {
		t.Errorf("certificate is not read from the primary: %v", read.body["delegate_to"])
	}
	slurp, _ := read.body["ansible.builtin.slurp"].(map[string]any)
	if slurp["src"] != "{{ faas_postgres_replication_tls_dir }}/server.crt" {
		t.Errorf("certificate read from %v", slurp["src"])
	}
	pin := "postgres_standby — pin the primary's replication certificate"
	if _, ok := index["postgres_standby — require the primary's replication certificate"]; !ok {
		t.Error("the standby does not fail clearly when the primary has no certificate")
	}
	for _, later := range []string{"postgres_standby — render the standby settings", "postgres_standby — seed the replica from the primary"} {
		if index[pin] == 0 || index[pin] > index[later] {
			t.Errorf("the certificate must be pinned before %q", later)
		}
	}
	dest, _ := byName[pin].body["ansible.builtin.copy"].(map[string]any)
	if dest["dest"] != "{{ faas_postgres_standby_primary_cert }}" {
		t.Errorf("pinned at %v", dest["dest"])
	}
	for _, name := range []string{"postgres_standby — confirm the replicator can reach the primary", "postgres_standby — copy the primary with pg_basebackup"} {
		env, _ := byName[name].body["environment"].(map[string]any)
		if env["PGSSLMODE"] != "verify-full" || env["PGSSLROOTCERT"] != "{{ faas_postgres_standby_primary_cert }}" {
			t.Errorf("%q does not verify the primary: %v", name, env)
		}
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "postgres_standby", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var defaults map[string]any
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults["faas_postgres_replication_tls_dir"] != replicationTLSDirDefault {
		t.Errorf("standby reads the certificate from %v, the primary writes it under %s", defaults["faas_postgres_replication_tls_dir"], replicationTLSDirDefault)
	}
	// A reseed empties the data directory; the pin must survive it.
	pinned, _ := defaults["faas_postgres_standby_primary_cert"].(string)
	if dataDir, _ := defaults["faas_postgres_standby_data_dir"].(string); pinned == "" || strings.HasPrefix(pinned, dataDir) {
		t.Errorf("pinned certificate %q lies inside the data directory %q", pinned, dataDir)
	}
}

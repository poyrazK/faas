// adr: 1152
// postgres_ha_role_test.go renders the postgres_ha templates through ansible
// for a three-member cell and pins the properties ADR-1152 §3 depends on: one
// synchronous standby, a required watchdog, WAL archiving through pgBackRest
// into two repositories, and HAProxy following Patroni's /primary check on
// every member.

package daemonunitspec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// adr: 1152
func TestPostgresHARoleRendersAThreeZoneCell(t *testing.T) {
	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not installed; render check skipped")
	}
	role := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "postgres_ha")
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	inventory := "[control_plane]\n" +
		"cp-b ansible_host=10.10.0.11 ansible_connection=local faas_host_zone=us-east1-b\n" +
		"cp-c ansible_host=10.10.0.12 ansible_connection=local faas_host_zone=us-east1-c\n" +
		"cp-d ansible_host=10.10.0.13 ansible_connection=local faas_host_zone=us-east1-d\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts.ini"), []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}
	// The role's defaults load through vars_files; the cell's values go in as
	// extra vars, which outrank them as inventory and vault values outrank a
	// role's defaults in a real run.
	extraVars, err := yaml.Marshal(map[string]any{
		"faas_cell_name":                        "us-east1",
		"faas_postgres_allowed_cidrs":           []string{"10.10.0.0/20"},
		"faas_postgres_ha_superuser_password":   "test-superuser-000001",
		"faas_postgres_ha_replication_password": "test-replication-0001",
		"faas_postgres_ha_rewind_password":      "test-rewind-00000001",
		"faas_postgres_ha_restapi_password":     "test-restapi-0000001",
		"faas_pgbackrest_repo1_bucket":          "pg-us-east1",
		"faas_pgbackrest_repo2_bucket":          "pg-us-west1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cell.yml"), extraVars, 0o600); err != nil {
		t.Fatal(err)
	}
	play := []map[string]any{{
		"hosts": "cp-b", "gather_facts": false,
		"vars_files": []string{filepath.Join(role, "defaults", "main.yml")},
		"tasks": []map[string]any{{
			"ansible.builtin.template": map[string]any{
				"src":  filepath.Join(role, "templates", "{{ item }}.j2"),
				"dest": filepath.Join(out, "{{ item }}"),
			},
			"loop": []string{"patroni.yml", "haproxy.cfg", "etcd.env", "pgbackrest.conf"},
		}},
	}}
	body, err := yaml.Marshal(play)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	playPath := filepath.Join(dir, "render.yml")
	if err := os.WriteFile(playPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(playbookBin, "-i", filepath.Join(dir, "hosts.ini"), "-e", "@"+filepath.Join(dir, "cell.yml"), playPath)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render postgres_ha templates: %v\n%s", err, output)
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	var patroni struct {
		Bootstrap struct {
			DCS struct {
				SynchronousMode      bool `yaml:"synchronous_mode"`
				SynchronousNodeCount int  `yaml:"synchronous_node_count"`
				PostgreSQL           struct {
					UsePgRewind bool           `yaml:"use_pg_rewind"`
					Parameters  map[string]any `yaml:"parameters"`
				} `yaml:"postgresql"`
			} `yaml:"dcs"`
		} `yaml:"bootstrap"`
		Etcd3 struct {
			Hosts    []string `yaml:"hosts"`
			Protocol string   `yaml:"protocol"`
		} `yaml:"etcd3"`
		Watchdog struct {
			Mode string `yaml:"mode"`
		} `yaml:"watchdog"`
		Tags map[string]any `yaml:"tags"`
	}
	if err := yaml.Unmarshal([]byte(read("patroni.yml")), &patroni); err != nil {
		t.Fatalf("patroni.yml is not YAML: %v", err)
	}
	dcs := patroni.Bootstrap.DCS
	if !dcs.SynchronousMode || dcs.SynchronousNodeCount != 1 || !dcs.PostgreSQL.UsePgRewind {
		t.Errorf("patroni must keep one synchronous standby with pg_rewind: %+v", dcs)
	}
	if got := dcs.PostgreSQL.Parameters["archive_command"]; !strings.Contains(got.(string), "pgbackrest") {
		t.Errorf("archive_command = %v, want pgBackRest", got)
	}
	if dcs.PostgreSQL.Parameters["archive_timeout"] != "60s" {
		t.Errorf("archive_timeout = %v, want 60s", dcs.PostgreSQL.Parameters["archive_timeout"])
	}
	if patroni.Watchdog.Mode != "required" {
		t.Errorf("watchdog mode = %q, want required", patroni.Watchdog.Mode)
	}
	if len(patroni.Etcd3.Hosts) != 3 || patroni.Etcd3.Protocol != "https" {
		t.Errorf("etcd3 = %+v, want three https members", patroni.Etcd3)
	}
	if patroni.Tags["zone"] != "us-east1-b" {
		t.Errorf("member zone tag = %v", patroni.Tags["zone"])
	}

	haproxy := read("haproxy.cfg")
	for _, want := range []string{
		"bind 127.0.0.1:5433", "uri /primary", "bind 127.0.0.1:5434", "uri /replica",
		"on-marked-down shutdown-sessions",
		"server cp-b 10.10.0.11:5432 check port 8008",
		"server cp-c 10.10.0.12:5432 check port 8008",
		"server cp-d 10.10.0.13:5432 check port 8008",
	} {
		if !strings.Contains(haproxy, want) {
			t.Errorf("haproxy.cfg missing %q:\n%s", want, haproxy)
		}
	}

	etcd := read("etcd.env")
	if !strings.Contains(etcd, `ETCD_INITIAL_CLUSTER="cp-b=https://10.10.0.11:2380,cp-c=https://10.10.0.12:2380,cp-d=https://10.10.0.13:2380"`) ||
		!strings.Contains(etcd, `ETCD_CLIENT_CERT_AUTH="true"`) || !strings.Contains(etcd, `ETCD_PEER_CLIENT_CERT_AUTH="true"`) {
		t.Errorf("etcd.env is not a three-member mutual-TLS cluster:\n%s", etcd)
	}

	backrest := read("pgbackrest.conf")
	for _, want := range []string{"repo1-gcs-bucket=pg-us-east1", "repo2-gcs-bucket=pg-us-west1", "archive-async=y"} {
		if !strings.Contains(backrest, want) {
			t.Errorf("pgbackrest.conf missing %q:\n%s", want, backrest)
		}
	}
}

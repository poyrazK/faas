// adr: 1027 — database hosts in the manifest become the database_nodes
// inventory, schedd's database scheduler, pgproxyd's cancel peers and the
// compute nodes' app route; a fleet without them renders as before.
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

const databaseFleetHosts = `    - name: fsn-1
      role: control-plane
      address: 10.42.0.1:7100
    - name: fsn-2
      role: compute-only
      address: 10.42.0.2:50051
    - name: db-1
      role: database
      address: 10.42.0.5:50051
      database:
        admission_ceiling_mb: 48000
        volume_capacity_bytes: 3000000000000
        boot_slots: 8
        postgres_majors: [16, 17]
    - name: db-2
      role: database
      address: 10.42.0.6:50051
      database:
        admission_ceiling_mb: 24000
        volume_capacity_bytes: 1000000000000
`

func databaseManifest(t *testing.T, managed string) *manifest.Manifest {
	t.Helper()
	yaml := strings.Replace(validManifestYAML, "    - name: fsn-1\n      role: control-plane\n", databaseFleetHosts, 1) + managed
	m, err := manifest.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}
	return m
}

func renderedFiles(t *testing.T, m *manifest.Manifest) map[string]string {
	t.Helper()
	files, err := renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[filepath.Base(f.Path)] = string(f.Body)
	}
	return out
}

func TestRenderManifestAnsibleFiles_DatabaseNodes(t *testing.T) {
	files := renderedFiles(t, databaseManifest(t, "managed_postgres:\n  host_suffix: db.gregale.dev\n  shared_archive: true\n  app_route: true\n"))
	if !strings.Contains(files["hosts.ini"], "[database_nodes]\ndb-1\ndb-2\n") {
		t.Fatalf("inventory lacks the database nodes:\n%s", files["hosts.ini"])
	}
	cp := files["fsn-1.yml"]
	for _, want := range []string{
		"faas_schedd_database_scheduler:\n  shared_archive: true\n  nodes:\n",
		"    - id: \"db-1\"\n      target_url: \"tcp://10.42.0.5:50051\"\n      admission_ceiling_mb: 48000\n      volume_capacity_bytes: 3000000000000\n      boot_slots: 8\n      postgres_majors: [16, 17]\n",
		"    - id: \"db-2\"\n      target_url: \"tcp://10.42.0.6:50051\"\n      admission_ceiling_mb: 24000\n      volume_capacity_bytes: 1000000000000\n",
		`faas_postgres_pgproxyd_cidrs: ["10.42.0.5/32", "10.42.0.6/32"]`,
		`faas_database_allowed_cidrs: ["10.42.0.5/32", "10.42.0.6/32"]`,
		"faas_database_allowed_ports: [5432, 7100]\n",
		"prom_database_targets:\n  - node: \"db-1\"\n    host: \"10.42.0.5\"\n  - node: \"db-2\"\n    host: \"10.42.0.6\"\n",
	} {
		if !strings.Contains(cp, want) {
			t.Errorf("control plane vars lack %q:\n%s", want, cp)
		}
	}
	db2 := files["db-2.yml"]
	for _, want := range []string{
		"faas_box_role: database\n",
		`database_vmmd_overlay_ip: "10.42.0.6"`,
		"database_vmmd_port: 50051\n",
		`database_node_overlay_cidrs: ["10.42.0.0/24"]`,
		`pgproxyd_host_suffix: "db.gregale.dev"`,
		`pgproxyd_schedd_target: "tcp://schedd.faas:7100"`,
		"pgproxyd_instance_id: 2\n",
		"pgproxyd_cancel_peers:\n  1: \"10.42.0.5:5432\"\n",
		`database_node_metrics_host: "10.42.0.6"`,
		`database_node_metrics_cidrs: ["10.42.0.1/32"]`,
		`pgproxyd_db_url: "postgres://faas_pgproxyd:{{ hostvars[groups['control_plane'] | first].faas_pgproxyd_database_password | urlencode | replace('/', '%2F') }}@10.42.0.1:5432/faas?sslmode=disable"`,
		"faas_private_hosts:",
	} {
		if !strings.Contains(db2, want) {
			t.Errorf("db-2 vars lack %q:\n%s", want, db2)
		}
	}
	if strings.Contains(db2, "faas_vmmd_target_url") || strings.Contains(db2, "faas_schedd_database_scheduler") {
		t.Errorf("db-2 vars carry another role's settings:\n%s", db2)
	}
	compute := files["fsn-2.yml"]
	for _, want := range []string{
		"faas_database_route_enabled: true\n",
		`gatewayd_database_route_listen: "{{ gatewayd_service_proxy_listen | regex_replace(':[0-9]+$', ':10083') }}"`,
		`gatewayd_database_route_upstreams: ["10.42.0.5:5432", "10.42.0.6:5432"]`,
		`gatewayd_database_route_host_suffix: "db.gregale.dev"`,
	} {
		if !strings.Contains(compute, want) {
			t.Errorf("compute vars lack %q:\n%s", want, compute)
		}
	}
	// Database nodes resolve the control plane's schedd.faas, and get no
	// role service name of their own.
	if !strings.Contains(db2, `names: ["fsn-1", "schedd.faas", "apid.faas"`) || strings.Contains(cp, `"db-1", "vmmd.faas"`) {
		t.Errorf("private hosts:\n%s", db2)
	}
}

func TestRenderManifestAnsibleFiles_DatabaseNodesWithoutAppRoute(t *testing.T) {
	files := renderedFiles(t, databaseManifest(t, "managed_postgres:\n  host_suffix: db.gregale.dev\n"))
	if strings.Contains(files["fsn-2.yml"], "database_route") {
		t.Fatalf("app route rendered without managed_postgres.app_route:\n%s", files["fsn-2.yml"])
	}
	if !strings.Contains(files["fsn-1.yml"], "  shared_archive: false\n") {
		t.Fatalf("control plane vars:\n%s", files["fsn-1.yml"])
	}
}

// A lost node stays in schedd's scheduler, which fails its databases over,
// but nothing else converges, scrapes or routes to it; a draining one is
// still served.
func TestRenderManifestAnsibleFiles_DatabaseNodeLifecycle(t *testing.T) {
	m := databaseManifest(t, "managed_postgres:\n  host_suffix: db.gregale.dev\n  shared_archive: true\n  app_route: true\n  evacuate_draining: true\n")
	m.Fleet.Hosts[2].Database.Lifecycle = manifest.DatabaseNodeDraining
	m.Fleet.Hosts[3].Database.Lifecycle = manifest.DatabaseNodeUnavailable
	files := renderedFiles(t, m)
	if !strings.Contains(files["hosts.ini"], "[database_nodes]\ndb-1\n") || strings.Contains(files["hosts.ini"], "db-2") {
		t.Fatalf("inventory:\n%s", files["hosts.ini"])
	}
	cp := files["fsn-1.yml"]
	for _, want := range []string{
		"  shared_archive: true\n  evacuate_draining: true\n",
		"      boot_slots: 8\n      lifecycle: \"draining\"\n",
		"      volume_capacity_bytes: 1000000000000\n      lifecycle: \"unavailable\"\n",
		"prom_database_targets:\n  - node: \"db-1\"\n    host: \"10.42.0.5\"\n",
	} {
		if !strings.Contains(cp, want) {
			t.Errorf("control plane vars lack %q:\n%s", want, cp)
		}
	}
	if strings.Contains(cp, `host: "10.42.0.6"`) {
		t.Errorf("the lost node is still scraped:\n%s", cp)
	}
	if db1 := files["db-1.yml"]; !strings.Contains(db1, "pgproxyd_cancel_peers: {}\n") {
		t.Errorf("db-1 still forwards cancels to the lost node:\n%s", db1)
	}
	if db2 := files["db-2.yml"]; !strings.Contains(db2, "pgproxyd_instance_id: 2\n") {
		t.Errorf("marking a node lost renumbered it:\n%s", db2)
	}
	if compute := files["fsn-2.yml"]; !strings.Contains(compute, `gatewayd_database_route_upstreams: ["10.42.0.5:5432"]`) {
		t.Errorf("the app route still dials the lost node:\n%s", compute)
	}

	// With every node lost the route keeps its upstreams: gatewayd-internal
	// refuses a route without any.
	m.Fleet.Hosts[2].Database.Lifecycle = manifest.DatabaseNodeUnavailable
	files = renderedFiles(t, m)
	if compute := files["fsn-2.yml"]; !strings.Contains(compute, `gatewayd_database_route_upstreams: ["10.42.0.5:5432", "10.42.0.6:5432"]`) {
		t.Errorf("app route with every node lost:\n%s", compute)
	}
	if cp := files["fsn-1.yml"]; !strings.Contains(cp, "prom_database_targets: []\n") || strings.Contains(files["hosts.ini"], "database_nodes") {
		t.Errorf("every node lost:\n%s\n%s", cp, files["hosts.ini"])
	}
}

// A fleet without database hosts renders exactly as before: no database
// group, no scheduler and no route.
func TestRenderManifestAnsibleFiles_NoDatabaseNodes(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n", 1)
	m, err := manifest.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range renderedFiles(t, m) {
		if strings.Contains(body, "database_nodes") || strings.Contains(body, "faas_schedd_database_scheduler") || strings.Contains(body, "database_route") || strings.Contains(body, "pgproxyd") {
			t.Errorf("%s carries database settings:\n%s", name, body)
		}
	}
}

// drill_every reaches schedd's database scheduler; "off" becomes schedd's
// negative spacing, and leaving it out keeps schedd's default.
func TestRenderManifestAnsibleFiles_DatabaseDrillEvery(t *testing.T) {
	for setting, want := range map[string]string{
		"  drill_every: 48h\n": "  drill_every: \"48h\"\n",
		"  drill_every: off\n": "  drill_every: \"-1s\"\n",
		"":                     "",
	} {
		m := databaseManifest(t, "managed_postgres:\n  host_suffix: db.gregale.dev\n  shared_archive: true\n"+setting)
		cp := renderedFiles(t, m)["fsn-1.yml"]
		if want == "" {
			if strings.Contains(cp, "drill_every") {
				t.Errorf("no setting rendered drill_every:\n%s", cp)
			}
			continue
		}
		if !strings.Contains(cp, want) {
			t.Errorf("%q: control plane vars lack %q:\n%s", setting, want, cp)
		}
	}
}

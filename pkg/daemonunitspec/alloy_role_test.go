package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAlloyKeepsTheShipperContract pins ADR-20261023185953. Alloy replaces
// Promtail, and the parts of the contract that are easy to lose in a
// translation are fixed here:
//   - the same stages in the same order;
//   - only the validated identity fields become labels (ADR-143);
//   - DEBUG stays local, and the stored line is the daemon's own;
//   - mTLS with a server-name override;
//   - the job label the dashboards select;
//   - usage reporting off, GA components only, and the HTTP server (which
//     includes a config UI) never on a wildcard address.
func TestAlloyKeepsTheShipperContract(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(roles, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	config := read("alloy/templates/config.alloy.j2")

	stages := regexp.MustCompile(`(?m)^\s+stage\.(\w+) \{`).FindAllStringSubmatch(config, -1)
	var order []string
	for _, m := range stages {
		order = append(order, m[1])
	}
	if got := strings.Join(order, ","); got != "json,json,drop,labels,output" {
		t.Errorf("loki.process stages = %s, want json,json,drop,labels,output (Promtail's pipeline)", got)
	}
	labels := config[strings.Index(config, "stage.labels {"):]
	labels = labels[:strings.Index(labels, "stage.output")]
	keys := regexp.MustCompile(`(?m)^\s+(\w+)\s+= ""`).FindAllStringSubmatch(labels, -1)
	var got []string
	for _, k := range keys {
		got = append(got, k[1])
	}
	if strings.Join(got, ",") != "account_id,app_id,daemon,level" {
		t.Errorf("stage.labels promotes %v; only account_id, app_id, daemon and level may become labels (ADR-143)", got)
	}
	for _, want := range []string{
		`value               = "DEBUG"`,
		`source = "message"`,
		`format_as_json = true`,
		`replacement  = "faas-journal"`,
		`server_name = "{{ alloy_loki_server_name }}"`,
		`cert_file   = "{{ alloy_loki_client_cert_file }}"`,
		`tenant_id           = "{{ alloy_loki_tenant_id }}"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config.alloy.j2 missing %q", want)
		}
	}

	unit := serviceSection(read("alloy/templates/alloy.service.j2"))
	for _, want := range []string{"--disable-reporting", "--stability.level=generally-available", "Slice=faas-obs.slice", "MemoryMax={{ alloy_memory_max }}"} {
		if !strings.Contains(unit, want) {
			t.Errorf("alloy.service missing %q", want)
		}
	}

	defaults := readRoleDefaults(t, filepath.Join(roles, "alloy"))
	if yamlScalar(defaults["alloy_listen_port"]) != "9080" {
		t.Errorf("alloy_listen_port = %v; apid's discovery scrapes host:9080", defaults["alloy_listen_port"])
	}
	if !strings.Contains(read("alloy/tasks/main.yml"), "alloy_effective_listen_address not in ['0.0.0.0', '::']") {
		t.Error("the alloy role no longer rejects a wildcard listen address")
	}
	if v := yamlScalar(defaults["alloy_release_sha256"]); len(v) != 64 {
		t.Errorf("alloy_release_sha256 = %q, want a SHA-256", v)
	}
}

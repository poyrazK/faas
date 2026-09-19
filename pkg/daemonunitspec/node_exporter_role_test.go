package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNodeExporterRolePinsXFSParserAndValidatesTheRestartedProcess protects the
// production failure found on Linux 7.0 XFS hosts. A scrape can return HTTP 200
// while one collector fails, and replacing a running binary does not restart
// the old inode by itself.
func TestNodeExporterRolePinsXFSParserAndValidatesTheRestartedProcess(t *testing.T) {
	root := repoRoot(t)
	defaults, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "node_exporter", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	defaultsText := string(defaults)
	for _, required := range []string{
		`node_exporter_version: "1.12.1"`,
		`node_exporter_release_sha256: "b51d8a76aa2a9156a55d501aca6276fae09e262259a5e4e831d2c2222f084e63"`,
	} {
		if !strings.Contains(defaultsText, required) {
			t.Errorf("node_exporter defaults missing %q", required)
		}
	}

	tasks, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "node_exporter", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	tasksText := string(tasks)
	for _, required := range []string{
		"notify: restart node_exporter",
		"ansible.builtin.meta: flush_handlers",
		`node_scrape_collector_success{collector="xfs"} 1`,
	} {
		if !strings.Contains(tasksText, required) {
			t.Errorf("node_exporter tasks missing %q", required)
		}
	}
}

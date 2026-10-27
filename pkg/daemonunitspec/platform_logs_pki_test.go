package daemonunitspec

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
)

// TestPlatformLogsTLSPathsMatchTheIssuedLeaves pins ADR-20261023124955 §2.
// The platform_logs_pki role issues the log pipeline's fleet-PKI feature
// leaves with `gregalectl pki issue-feature`, and Loki, Promtail, Prometheus,
// Grafana and gregalectl obs logs read them from role defaults. A path that
// drifts from what pkg/pki writes fails only at run time on a live host, as
// a missing-file assert or a TLS handshake error, so pin them here.
func TestPlatformLogsTLSPathsMatchTheIssuedLeaves(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	leaf := func(dir, file string) (string, string) {
		for _, r := range pki.FeatureRoles(pki.FeaturePlatformLogs, "single-box") {
			if r.Directory == dir && r.Filename == file {
				return pki.LeafPaths(pki.DefaultRootDir, r)
			}
		}
		t.Fatalf("pkg/pki issues no %s/%s platform-logs leaf", dir, file)
		return "", ""
	}
	ca, _ := pki.CARoot(pki.DefaultRootDir)

	for _, tc := range []struct {
		role, dir, file, caKey, certKey, keyKey string
	}{
		{"loki", "loki", "server", "loki_tls_ca_file", "loki_tls_cert_file", "loki_tls_key_file"},
		{"loki", "prometheus", "loki-client", "loki_tls_ca_file", "loki_cli_client_cert_file", "loki_cli_client_key_file"},
		{"promtail", "promtail", "loki-client", "promtail_loki_ca_file", "promtail_loki_client_cert_file", "promtail_loki_client_key_file"},
		{"prometheus", "prometheus", "loki-client", "prom_loki_metrics_ca_file", "prom_loki_metrics_client_cert_file", "prom_loki_metrics_client_key_file"},
		{"grafana", "grafana", "loki-client", "gv_loki_ca_file", "gv_loki_client_cert_file", "gv_loki_client_key_file"},
	} {
		defaults := readRoleDefaults(t, filepath.Join(roles, tc.role))
		cert, key := leaf(tc.dir, tc.file)
		for name, want := range map[string]string{tc.caKey: ca, tc.certKey: cert, tc.keyKey: key} {
			if got := yamlScalar(defaults[name]); got != want {
				t.Errorf("%s default %s = %q, want %q (what pkg/pki issues)", tc.role, name, got, want)
			}
		}
	}

	// The issuing role's leaf table must name exactly the leaves pkg/pki issues.
	pkiDefaults := readRoleDefaults(t, filepath.Join(roles, "platform_logs_pki"))
	raw, _ := pkiDefaults["faas_platform_logs_leaves"].([]any)
	var listed, issued []string
	for _, item := range raw {
		m, _ := item.(map[string]any)
		path, _ := m["path"].(string)
		listed = append(listed, path)
	}
	for _, r := range pki.FeatureRoles(pki.FeaturePlatformLogs, "single-box") {
		issued = append(issued, r.Directory+"/"+r.Filename)
	}
	sort.Strings(listed)
	sort.Strings(issued)
	if strings.Join(listed, ",") != strings.Join(issued, ",") {
		t.Errorf("faas_platform_logs_leaves = %v, pkg/pki issues %v", listed, issued)
	}
}

// TestPlatformLogsRestartToleratesUninstalledUnits pins the fix the first
// obs-stack-smoke run found: on the first converge with logs on, this role
// runs before loki, alloy and prometheus are installed, and `systemctl
// try-restart` exits 5 ("unit not found"). That must not fail the play.
func TestPlatformLogsRestartToleratesUninstalledUnits(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "platform_logs_pki", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	tasks := string(body)
	i := strings.Index(tasks, "- name: platform_logs_pki — restart the services whose leaf changed")
	if i < 0 {
		t.Fatal("restart task is missing")
	}
	task := tasks[i:]
	if j := strings.Index(task[1:], "\n- name:"); j >= 0 {
		task = task[:j+1]
	}
	for _, want := range []string{"try-restart", "register: faas_platform_logs_restart", "failed_when: faas_platform_logs_restart.rc not in [0, 5]"} {
		if !strings.Contains(task, want) {
			t.Errorf("restart task lacks %q:\n%s", want, task)
		}
	}
}

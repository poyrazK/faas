package daemonunitspec

// adr: 1323

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without fair use realtimed keeps the 10,000-connection node default, which
// does not fit its 512M MemoryMax, so both flags are protections every unit
// ships with. The gatewayd_internal_service role's realtimed unit is kept by
// hand, not by the generator, so it is checked here too.
func TestRealtimeUnitsEnableFairUse(t *testing.T) {
	root := repoRoot(t)
	fairUse := "Environment=FAAS_REALTIME_FAIR_USE_ENABLED=1\n"
	budget := "Environment=FAAS_REALTIME_OUTBOUND_BUDGET_ENABLED=1\n"
	for path, want := range map[string][]string{
		filepath.Join(root, "deploy", "systemd", "faas-apid.service"):                                                     {fairUse},
		filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-apid.service"):          {fairUse},
		filepath.Join(root, "deploy", "systemd", "faas-realtimed.service"):                                                {fairUse, budget},
		filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_internal_service", "files", "faas-realtimed.service"): {fairUse, budget},
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range want {
			if !strings.Contains(string(body), line) {
				t.Errorf("%s lacks %q", path, strings.TrimSpace(line))
			}
		}
	}
}

// Fair use syncs connection counts through apid. On a compute-only host the
// default target is the single-box Unix socket, which only the control plane
// has, so every sync failed and plan limits held per node (Hunt 14 H14-110).
// The role drop-in points realtimed at apid's private mTLS listener.
func TestRealtimedComputeOnlyHistoryTarget(t *testing.T) {
	tmpl, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_internal_service", "templates", "99-faas-realtimed-role.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "realtimed", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{
		"faas_box_role":                     "compute-only",
		"gatewayd_app_errors_target":        "tcp://apid.faas:9093",
		"gatewayd_app_errors_tls_cert_path": "/etc/faas/tls/gatewayd/apid-client.crt",
		"gatewayd_app_errors_tls_key_path":  "/etc/faas/tls/gatewayd/apid-client.key",
		"gatewayd_app_errors_tls_ca_path":   "/etc/faas/tls/ca/ca.crt",
	}
	split := renderPublicEdgeBlock(t, string(tmpl), vars)
	for _, want := range []string{
		"Environment=FAAS_REALTIME_HISTORY_TARGET=tcp://apid.faas:9093",
		"Environment=FAAS_REALTIME_HISTORY_TLS_CERT_PATH=/etc/faas/tls/gatewayd/apid-client.crt",
		"Environment=FAAS_REALTIME_HISTORY_TLS_KEY_PATH=/etc/faas/tls/gatewayd/apid-client.key",
		"Environment=FAAS_REALTIME_HISTORY_TLS_CA_PATH=/etc/faas/tls/ca/ca.crt",
	} {
		if !strings.Contains(split, want+"\n") {
			t.Errorf("compute-only drop-in lacks %q:\n%s", want, split)
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(want, "Environment="), "=")
		if !strings.Contains(string(src), `"`+name+`"`) {
			t.Errorf("realtimed does not read %s", name)
		}
	}
	vars["faas_box_role"] = "single-box"
	if single := renderPublicEdgeBlock(t, string(tmpl), vars); strings.Contains(single, "FAAS_REALTIME_HISTORY_") {
		t.Errorf("single-box drop-in overrides the Unix history socket:\n%s", single)
	}
	if bare := renderPublicEdgeBlock(t, string(tmpl), map[string]any{"faas_box_role": "compute-only"}); strings.Contains(bare, "FAAS_REALTIME_HISTORY_") {
		t.Errorf("compute-only drop-in without an apid target sets history:\n%s", bare)
	}
}

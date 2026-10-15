package daemonunitspec

// adr: 1189

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestScheddUnitEnablesPrewarm(t *testing.T) {
	if !hasEnvironment(UnitSchedd(), "FAAS_PREWARM_ENABLED", "1") {
		t.Fatal("schedd unit must enable the scheduled prewarm drain (ADR-1189)")
	}
}

func TestControlPlaneRoleProvisionsSafeReleaseTokens(t *testing.T) {
	root := repoRoot(t)
	role := filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service")
	var task map[string]any
	for _, tk := range flattenRoleTasks("main.yml", loadYAMLList(t, filepath.Join(role, "tasks", "main.yml"))) {
		if script, ok := tk.body["ansible.builtin.script"].(map[string]any); ok && strings.Contains(script["cmd"].(string), "provision-safe-release-tokens.sh") {
			task = tk.body
		}
	}
	if task == nil {
		t.Fatal("control_plane_service must provision the Safe Deploy token pair")
	}
	cmd := task["ansible.builtin.script"].(map[string]any)["cmd"].(string)
	if !strings.Contains(cmd, "/etc/faas/sealed.env /etc/faas/secrets/meterd/billing.env") {
		t.Fatalf("script must write the files APID and meterd read: %q", cmd)
	}
	if task["no_log"] != true || task["when"] != "faas_safe_releases_enabled | bool" {
		t.Fatalf("task must hide output and honour the opt-out: no_log=%v when=%v", task["no_log"], task["when"])
	}
	var notify []string
	for _, n := range task["notify"].([]any) {
		notify = append(notify, n.(string))
	}
	if !slices.Contains(notify, "restart faas-apid") || !slices.Contains(notify, "restart faas-meterd") {
		t.Fatalf("notify = %v", notify)
	}
	defaults, err := os.ReadFile(filepath.Join(role, "defaults", "main.yml"))
	if err != nil || !regexp.MustCompile(`(?m)^faas_safe_releases_enabled: true$`).Match(defaults) {
		t.Fatalf("faas_safe_releases_enabled must default to true (%v)", err)
	}
	info, err := os.Stat(filepath.Join(role, "files", "provision-safe-release-tokens.sh"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("script must exist and be executable: %v %v", info, err)
	}
}

// runTokenScript runs the provisioning script on two env files.
func runTokenScript(t *testing.T, sealed, meterd string) (string, error) {
	t.Helper()
	for _, tool := range []string{"bash", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	script := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "control_plane_service", "files", "provision-safe-release-tokens.sh")
	out, err := exec.Command("bash", script, sealed, meterd).CombinedOutput()
	return string(out), err
}

func writeEnv(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envValue(t *testing.T, path, name string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := ""
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(line, name+"="); ok {
			value = v
		}
	}
	return value
}

func TestProvisionSafeReleaseTokensScript(t *testing.T) {
	dir := t.TempDir()
	sealed, meterd := filepath.Join(dir, "sealed.env"), filepath.Join(dir, "billing.env")
	// sealed.env lacks a trailing newline: the token must land on its own line.
	writeEnv(t, sealed, "DATABASE_URL=postgres:///faas")
	writeEnv(t, meterd, "FAAS_MAIL_TRANSPORT=resend\n")

	out, err := runTokenScript(t, sealed, meterd)
	if err != nil || !strings.HasSuffix(strings.TrimSpace(out), "changed=1") {
		t.Fatalf("fresh run: %v %q", err, out)
	}
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	canary, action := envValue(t, sealed, "FAAS_CANARY_PROGRESSION_TOKEN"), envValue(t, sealed, "FAAS_SAFEDEPLOY_TOKEN")
	if !hex.MatchString(canary) || !hex.MatchString(action) || canary == action {
		t.Fatalf("tokens must be distinct 32-byte hex values: %q %q", canary, action)
	}
	if envValue(t, meterd, "FAAS_CANARY_PROGRESSION_TOKEN") != canary || envValue(t, meterd, "FAAS_SAFEDEPLOY_TOKEN") != action {
		t.Fatal("meterd must receive the same pair as APID")
	}
	if envValue(t, sealed, "DATABASE_URL") != "postgres:///faas" || envValue(t, meterd, "FAAS_MAIL_TRANSPORT") != "resend" {
		t.Fatal("existing lines must stay intact")
	}
	if strings.Contains(out, canary) || strings.Contains(out, action) {
		t.Fatal("the script must never print a token")
	}
	before, _ := os.ReadFile(sealed)
	if out, err := runTokenScript(t, sealed, meterd); err != nil || !strings.HasSuffix(strings.TrimSpace(out), "changed=0") {
		t.Fatalf("rerun: %v %q", err, out)
	}
	if after, _ := os.ReadFile(sealed); string(after) != string(before) {
		t.Fatal("a rerun must not change the files")
	}
	if info, _ := os.Stat(meterd); info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode changed to %v", info.Mode().Perm())
	}
}

func TestProvisionSafeReleaseTokensScriptKeepsAndChecksExistingValues(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	tests := []struct {
		name, sealed, meterd, wantErr string
	}{
		{"copies to meterd", "FAAS_CANARY_PROGRESSION_TOKEN=" + a + "\nFAAS_SAFEDEPLOY_TOKEN=" + b + "\n", "", ""},
		{"copies to apid", "", "FAAS_CANARY_PROGRESSION_TOKEN=" + a + "\nFAAS_SAFEDEPLOY_TOKEN=" + b + "\n", ""},
		{"mismatch", "FAAS_SAFEDEPLOY_TOKEN=" + a + "\n", "FAAS_SAFEDEPLOY_TOKEN=" + b + "\n", "differs"},
		{"same token twice", "FAAS_CANARY_PROGRESSION_TOKEN=" + a + "\nFAAS_SAFEDEPLOY_TOKEN=" + a + "\n", "", "must differ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			sealed, meterd := filepath.Join(dir, "sealed.env"), filepath.Join(dir, "billing.env")
			writeEnv(t, sealed, tt.sealed)
			writeEnv(t, meterd, tt.meterd)
			out, err := runTokenScript(t, sealed, meterd)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(out, tt.wantErr) {
					t.Fatalf("want failure %q, got %v %q", tt.wantErr, err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v %q", err, out)
			}
			for _, path := range []string{sealed, meterd} {
				if envValue(t, path, "FAAS_CANARY_PROGRESSION_TOKEN") != a || envValue(t, path, "FAAS_SAFEDEPLOY_TOKEN") != b {
					t.Fatalf("%s must hold the existing pair", path)
				}
			}
		})
	}
	dir := t.TempDir()
	if out, err := runTokenScript(t, filepath.Join(dir, "missing.env"), filepath.Join(dir, "also-missing.env")); err == nil || !strings.Contains(out, "missing") {
		t.Fatalf("missing files must fail: %v %q", err, out)
	}
}

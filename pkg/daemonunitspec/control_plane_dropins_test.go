package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bundleHeredoc returns the body cd-controlplane.yml writes with
// `cat > ".../<dropin>" <<'EOF'` into the signed control-plane bundle.
func bundleHeredoc(t *testing.T, workflow, dropin string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "cat > ") || !strings.Contains(line, "/"+dropin+`" <<'EOF'`) {
			continue
		}
		var body strings.Builder
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "EOF" {
				return body.String()
			}
			body.WriteString(strings.TrimSpace(next))
			body.WriteString("\n")
		}
		t.Fatalf("heredoc for %s is not terminated", dropin)
	}
	t.Fatalf("cd-controlplane.yml writes no %s into the bundle", dropin)
	return ""
}

// renderRoleTemplate substitutes the role defaults into a template that uses
// only plain `{{ variable }}` expressions.
func renderRoleTemplate(t *testing.T, root, name string) string {
	t.Helper()
	tmpl, err := os.ReadFile(filepath.Join(root, "templates", name))
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := os.ReadFile(filepath.Join(root, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`\{\{\s*([a-z0-9_]+)\s*\}\}`).ReplaceAllStringFunc(string(tmpl), func(expr string) string {
		name := strings.Trim(expr, "{} ")
		m := regexp.MustCompile(`(?m)^` + name + `:\s*(.+)$`).FindStringSubmatch(string(defaults))
		if m == nil {
			t.Fatalf("%s uses %s, which the role does not default", filepath.Base(name), name)
		}
		return strings.Trim(strings.TrimSpace(m[1]), `"'`)
	})
}

// TestAnsibleAndBundleRenderTheSameControlPlaneDropIns keeps the two writers of
// each control-plane drop-in byte-identical. Ansible's control_plane_service
// role and the signed bundle (cd-controlplane.yml, installed by deployctl)
// both write these files. deployctl installs an unchanged file without touching
// it, and it hands apid off only while no unit input is newer than the running
// process. Different renders made each CP stage rewrite the file twice, so
// deployctl restarted apid on every release (rc.331–rc.334); the ~10 s restart
// failed the CP stage's API gate.
func TestAnsibleAndBundleRenderTheSameControlPlaneDropIns(t *testing.T) {
	root := filepath.Join("..", "..", "deploy", "ansible", "roles", "control_plane_service")
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dropin := range []string{"zz-faas-billing-mode.conf", "zz-faas-public-status.conf"} {
		t.Run(dropin, func(t *testing.T) {
			// The bundle writes the environment's billing mode over a
			// placeholder (scripts/ci/faas_billing_mode.sh resolve), and the
			// same script stages faas_billing_mode for Ansible, so both render
			// one value. Compare at the role default.
			bundle := strings.ReplaceAll(bundleHeredoc(t, string(workflow), dropin), "@FAAS_BILLING_MODE@", roleDefault(t, root, "faas_billing_mode"))
			ansible := renderRoleTemplate(t, root, dropin+".j2")
			if ansible != bundle {
				t.Fatalf("Ansible renders %q but the bundle installs %q; deployctl would restart apid on every release", ansible, bundle)
			}
		})
	}
}

// roleDefault returns a control_plane_service role default.
func roleDefault(t *testing.T, root, name string) string {
	t.Helper()
	defaults, err := os.ReadFile(filepath.Join(root, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^` + name + `:\s*(.+)$`).FindStringSubmatch(string(defaults))
	if m == nil {
		t.Fatalf("role does not default %s", name)
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
}

// TestBundleBillingModeFollowsTheEnvironment pins that the signed bundle
// writes the environment's FAAS_BILLING_MODE (validated by
// scripts/ci/faas_billing_mode.sh) instead of a hard-coded value, and that
// both Ansible staging sites stage the same setting (billing launch review
// 2026-11-10: production billing was hard-coded off).
func TestBundleBillingModeFollowsTheEnvironment(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-controlplane.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, want := range []string{
		`billing_mode="$(bash scripts/ci/faas_billing_mode.sh resolve)"`,
		`sed -i "s/@FAAS_BILLING_MODE@/${billing_mode}/"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("cd-controlplane.yml lacks %q", want)
		}
	}
	if got := strings.Count(text, `bash scripts/ci/faas_billing_mode.sh vars "$work/vars.yml"`); got != 2 {
		t.Errorf("Ansible staging sites stage faas_billing_mode %d times, want 2", got)
	}
	if got := strings.Count(text, "FAAS_BILLING_MODE_SETTING: ${{ vars.FAAS_BILLING_MODE }}"); got != 3 {
		t.Errorf("steps reading vars.FAAS_BILLING_MODE = %d, want 3 (bundle + two Ansible steps)", got)
	}
	if strings.Contains(text, "Environment=FAAS_BILLING_MODE=disabled") {
		t.Error("the bundle still hard-codes FAAS_BILLING_MODE=disabled")
	}
}

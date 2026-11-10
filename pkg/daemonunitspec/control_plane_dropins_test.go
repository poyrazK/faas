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
			bundle := bundleHeredoc(t, string(workflow), dropin)
			ansible := renderRoleTemplate(t, root, dropin+".j2")
			if ansible != bundle {
				t.Fatalf("Ansible renders %q but the bundle installs %q; deployctl would restart apid on every release", ansible, bundle)
			}
		})
	}
}

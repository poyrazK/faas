// adr: 20261110120000 — wildcard listeners refuse to start without the host firewall.
package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var hostFirewallGuardTemplates = []string{
	"gatewayd_internal_service/templates/60-faas-require-host-firewall.conf.j2",
	"compute_only_service/templates/60-faas-require-host-firewall.conf.j2",
}

// renderGuard renders like Ansible for this template: plain jinja2 plus
// Ansible's bool filter, which accepts the "True"/"False" strings a templated
// task var can arrive as.
func renderGuard(t *testing.T, rel string, vars map[string]any) string {
	t.Helper()
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for template rendering")
	}
	encoded, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
env = Environment(trim_blocks=True)
env.filters["bool"] = lambda v: v if isinstance(v, bool) else str(v).strip().lower() in ("yes", "on", "1", "true")
print(env.from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	path := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", rel)
	out, err := exec.Command("python3", "-c", script, path, string(encoded)).CombinedOutput()
	if err != nil {
		t.Fatalf("render %s: %v: %s", rel, err, out)
	}
	return string(out)
}

func TestHostFirewallGuardRendersPerSection(t *testing.T) {
	for _, rel := range hostFirewallGuardTemplates {
		for _, section := range []string{"Service", "Socket"} {
			t.Run(filepath.Dir(filepath.Dir(rel))+"/"+section, func(t *testing.T) {
				out := renderGuard(t, rel, map[string]any{"faas_unit_section": section, "faas_require_host_firewall_on": "True"})
				if !strings.Contains(out, "\n["+section+"]\n") {
					t.Fatalf("want [%s] section in:\n%s", section, out)
				}
				// "+" runs the check with full privileges: nft needs CAP_NET_ADMIN
				// and gatewayd-internal and schedd run unprivileged.
				for _, want := range []string{"ExecStartPre=+/bin/sh -c '", "nft list chain inet faas input", `grep -q "policy drop"`, "exit 1"} {
					if !strings.Contains(out, want) {
						t.Fatalf("want %q in:\n%s", want, out)
					}
				}
				// systemd expands $ and % in Exec lines; the check must use neither.
				line := out[strings.Index(out, "ExecStartPre="):]
				line = line[:strings.Index(line, "\n")]
				if strings.ContainsAny(line, "$%") {
					t.Fatalf("Exec line uses a systemd-expanded character: %s", line)
				}
			})
		}
	}
}

func TestHostFirewallGuardOffRendersNoCheck(t *testing.T) {
	for _, rel := range hostFirewallGuardTemplates {
		// "False" is how a templated task var can arrive; it must not read as true.
		for _, off := range []any{false, "False"} {
			out := renderGuard(t, rel, map[string]any{"faas_unit_section": "Service", "faas_require_host_firewall_on": off})
			if strings.Contains(out, "ExecStartPre") || strings.Contains(out, "[Service]") {
				t.Fatalf("%s (%v): guard off still renders a check:\n%s", rel, off, out)
			}
		}
	}
}

// The guard is copied into both roles; the copies must not drift.
func TestHostFirewallGuardCopiesMatch(t *testing.T) {
	var first string
	for i, rel := range hostFirewallGuardTemplates {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", rel))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(raw)
		} else if string(raw) != first {
			t.Fatalf("%s differs from %s", rel, hostFirewallGuardTemplates[0])
		}
	}
}

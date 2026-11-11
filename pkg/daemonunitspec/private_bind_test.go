// adr: 20261110120000 — control-plane private listeners bind the private address.
package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var privateBindTemplates = []struct {
	rel, listenVar, listen, env string
}{
	{"control_plane_service/templates/99-faas-app-errors.conf.j2", "faas_apid_app_errors_listen", "tcp://0.0.0.0:9093", "FAAS_APID_APP_ERRORS_TARGET"},
	{"githubd_service/templates/99-faas-source-ref-mtls.conf.j2", "faas_githubd_listen_addr", "tcp://0.0.0.0:50053", "FAAS_GITHUBD_LISTEN_ADDR"},
}

func renderPrivateBindTemplate(t *testing.T, rel string, vars map[string]any) string {
	t.Helper()
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for template rendering")
	}
	encoded, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json, pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text(), trim_blocks=True).render(**json.loads(sys.argv[2])), end="")
`
	path := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", rel)
	out, err := exec.Command("python3", "-c", script, path, string(encoded)).CombinedOutput()
	if err != nil {
		t.Fatalf("render %s: %v: %s", rel, err, out)
	}
	return string(out)
}

func TestControlPlanePrivateListenersBindThePrivateAddress(t *testing.T) {
	facts := map[string]any{"default_ipv4": map[string]any{"address": "10.20.0.1"}, "all_ipv4_addresses": []string{"10.20.0.1", "203.0.113.7"}}
	cases := []struct {
		name string
		vars map[string]any
		want string
	}{
		{"published private address on the host", map[string]any{"faas_private_dns_address": "10.20.0.1", "ansible_facts": facts}, "10.20.0.1"},
		{"provider inventory address", map[string]any{"faas_private_address": "10.20.0.1", "ansible_facts": facts}, "10.20.0.1"},
		{"gathered default address", map[string]any{"ansible_facts": facts}, "10.20.0.1"},
		{"address not on this host", map[string]any{"faas_private_dns_address": "10.20.0.9", "ansible_facts": facts}, "0.0.0.0"},
		{"no facts", map[string]any{"faas_private_dns_address": "10.20.0.1"}, "0.0.0.0"},
	}
	for _, tpl := range privateBindTemplates {
		for _, tc := range cases {
			t.Run(filepath.Base(tpl.rel)+"/"+tc.name, func(t *testing.T) {
				vars := map[string]any{tpl.listenVar: tpl.listen}
				for k, v := range tc.vars {
					vars[k] = v
				}
				out := renderPrivateBindTemplate(t, tpl.rel, vars)
				port := tpl.listen[strings.LastIndex(tpl.listen, ":"):]
				want := "Environment=" + tpl.env + "=tcp://" + tc.want + port + "\n"
				if !strings.Contains(out, want) {
					t.Fatalf("want %q in:\n%s", want, out)
				}
			})
		}
	}
}

// The bind-address logic is copied into both templates; they must not drift.
func TestControlPlanePrivateBindCopiesMatch(t *testing.T) {
	block := regexp.MustCompile(`(?m)^\{% set faas_bind_candidate .*\n\{% set faas_private_bind .*$`)
	var got []string
	for _, tpl := range privateBindTemplates {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", tpl.rel))
		if err != nil {
			t.Fatal(err)
		}
		m := block.Find(raw)
		if m == nil {
			t.Fatalf("%s has no private bind block", tpl.rel)
		}
		got = append(got, string(m))
	}
	if got[0] != got[1] {
		t.Fatalf("private bind logic differs:\n%s\n---\n%s", got[0], got[1])
	}
}

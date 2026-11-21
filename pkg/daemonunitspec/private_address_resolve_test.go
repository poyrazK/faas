// adr: 20261109100000 — amendment 2026-11-19: the private address comes from the private card.
package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const privateAddressResolver = "deploy/ansible/tasks/resolve_private_address.yml"

// resolverExpression returns the Jinja expression the resolver task assigns to
// faas_private_dns_address.
func resolverExpression(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), privateAddressResolver))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := yaml.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("parse %s: %v", privateAddressResolver, err)
	}
	for _, task := range tasks {
		args, ok := task["ansible.builtin.set_fact"].(map[string]any)
		if !ok {
			continue
		}
		if expr, ok := args["faas_private_dns_address"].(string); ok {
			return expr
		}
	}
	t.Fatalf("%s sets no faas_private_dns_address", privateAddressResolver)
	return ""
}

func renderResolver(t *testing.T, expr string, vars map[string]any) string {
	t.Helper()
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for template rendering")
	}
	encoded, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json, sys
from jinja2 import Environment, StrictUndefined
print(Environment(undefined=StrictUndefined).from_string(sys.argv[1]).render(**json.loads(sys.argv[2])), end="")
`
	out, err := exec.Command("python3", "-c", script, expr, string(encoded)).CombinedOutput()
	if err != nil {
		t.Fatalf("render resolver: %v: %s", err, out)
	}
	return string(out)
}

// An OVH host's default route is its public card. With the vRack card named,
// the private address must be the card's, never the default route's.
func TestPrivateAddressResolverPrefersTheNamedCard(t *testing.T) {
	facts := map[string]any{
		"default_ipv4": map[string]any{"address": "203.0.113.10", "interface": "eno1np0"},
		"vrack0":       map[string]any{"ipv4": map[string]any{"address": "10.20.0.2"}},
		"eno2_4000":    map[string]any{"ipv4": map[string]any{"address": "10.30.0.2"}},
		"novlan":       map[string]any{"device": "novlan"},
	}
	cases := []struct {
		name string
		vars map[string]any
		want string
	}{
		{"provider inventory address wins", map[string]any{"faas_private_address": "10.20.0.9", "faas_private_iface": "vrack0", "ansible_facts": facts}, "10.20.0.9"},
		{"named card", map[string]any{"faas_private_iface": "vrack0", "ansible_facts": facts}, "10.20.0.2"},
		{"named VLAN card uses the fact key", map[string]any{"faas_private_iface": "eno2.4000", "ansible_facts": facts}, "10.30.0.2"},
		{"named card without IPv4 resolves empty", map[string]any{"faas_private_iface": "novlan", "ansible_facts": facts}, ""},
		{"named card missing resolves empty", map[string]any{"faas_private_iface": "vrack9", "ansible_facts": facts}, ""},
		{"no card keeps the default route", map[string]any{"faas_private_iface": "", "ansible_facts": facts}, "203.0.113.10"},
		{"card variable absent keeps the default route", map[string]any{"ansible_facts": facts}, "203.0.113.10"},
		{"empty inventory address is ignored", map[string]any{"faas_private_address": "", "ansible_facts": facts}, "203.0.113.10"},
		{"no facts resolves empty", map[string]any{}, ""},
	}
	expr := resolverExpression(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.TrimSpace(renderResolver(t, expr, tc.vars)); got != tc.want {
				t.Fatalf("resolved %q, want %q", got, tc.want)
			}
		})
	}
}

// Every playbook that first works out the private address uses the resolver,
// so no copy of the old default-route-only chain comes back.
func TestPrivateAddressIsResolvedOnlyByTheSharedTask(t *testing.T) {
	root := repoRoot(t)
	setFact := regexp.MustCompile(`(?m)^\s+faas_private_dns_address:`)
	users := map[string]bool{
		"deploy/ansible/preflight.yml":                     false,
		"deploy/ansible/bootstrap.yml":                     false,
		"deploy/ansible/postgres_standby.yml":              false,
		"deploy/ansible/pki_renew.yml":                     false,
		"deploy/ansible/roles/fleet_verify/tasks/main.yml": false,
	}
	err := filepath.WalkDir(filepath.Join(root, "deploy/ansible"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".cache" || d.Name() == ".generated" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yml") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel != privateAddressResolver && setFact.Match(body) {
			t.Errorf("%s sets faas_private_dns_address itself; include %s instead", rel, filepath.Base(privateAddressResolver))
		}
		if _, ok := users[rel]; ok {
			users[rel] = strings.Contains(string(body), "resolve_private_address.yml")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, includes := range users {
		if !includes {
			t.Errorf("%s does not include %s", rel, filepath.Base(privateAddressResolver))
		}
	}
}

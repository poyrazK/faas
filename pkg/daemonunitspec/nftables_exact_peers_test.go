// adr: 20261110120000 — the firewall admits each fleet peer by its own address.
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

func exactPeerVars(hostvars map[string]any) map[string]any {
	return map[string]any{
		"faas_control_plane_allowed_cidrs": []string{"10.20.0.0/24"},
		"faas_control_plane_allowed_exact": map[string]any{"cidrs": []string{}, "hosts": []string{"cp"}},
		"faas_compute_peer_exact":          map[string]any{"cidrs": []string{"10.20.0.9/32"}, "hosts": []string{"c2"}},
		"faas_compute_allowed_cidrs":       []string{"10.20.0.0/24"},
		"faas_postgres_allowed_cidrs":      []string{"10.20.0.0/24"},
		"faas_compute_allowed_exact":       map[string]any{"cidrs": []string{}, "hosts": []string{"c2"}},
		"hostvars":                         hostvars,
	}
}

func TestHostFirewallAdmitsExactPeers(t *testing.T) {
	out := renderHostFirewall(t, exactPeerVars(map[string]any{
		"cp": map[string]any{"faas_private_dns_address": "10.20.0.1"},
		"c2": map[string]any{"ansible_facts": map[string]any{"default_ipv4": map[string]any{"address": "10.20.0.3"}}},
	}))
	for _, want := range []string{
		`ip saddr 10.20.0.1/32 tcp dport { 50051 } accept comment "faas compute services from control"`,
		`ip saddr 10.20.0.9/32 tcp dport { 50051, 9091 } accept comment "faas vmmd and schedd from peer compute nodes`,
		`ip saddr 10.20.0.3/32 tcp dport { 50051, 9091 } accept comment "faas vmmd and schedd from peer compute nodes`,
		`ip saddr 10.20.0.3/32 tcp dport 5432 accept`,
		`ip saddr 10.20.0.3/32 tcp dport { 8081 } accept`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("firewall lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "10.20.0.0/24") {
		t.Errorf("resolved peers still admit the subnet:\n%s", out)
	}
}

// A peer whose address is not known in this run keeps that list as wide as
// before instead of cutting the host off.
func TestHostFirewallFallsBackWhenAPeerDoesNotResolve(t *testing.T) {
	out := renderHostFirewall(t, exactPeerVars(map[string]any{"cp": map[string]any{"faas_private_dns_address": "10.20.0.1"}}))
	for _, want := range []string{
		`ip saddr 10.20.0.1/32 tcp dport { 50051 } accept comment "faas compute services from control"`,
		`ip saddr 10.20.0.0/24 tcp dport { 50051, 9091 } accept comment "faas vmmd and schedd from peer compute nodes`,
		`ip saddr 10.20.0.0/24 tcp dport 5432 accept`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("firewall lacks %q:\n%s", want, out)
		}
	}
}

// Inventories without the exact lists render as before: no peer rule.
func TestHostFirewallWithoutExactListsIsUnchanged(t *testing.T) {
	out := renderHostFirewall(t, map[string]any{"faas_control_plane_allowed_cidrs": []string{"10.20.0.0/24"}})
	if strings.Contains(out, "peer compute nodes") || !strings.Contains(out, `ip saddr 10.20.0.0/24 tcp dport { 50051 } accept`) {
		t.Errorf("legacy inventory render changed:\n%s", out)
	}
}

// vmmd replaces the ruleset at runtime, so its drop-in must carry the same
// sources: the control plane's exact addresses and the peers, with the wider
// FAAS_PRIVATE_INGRESS_CIDRS kept for a vmmd that predates them.
func TestVmmdDropInCarriesExactPeers(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	path := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "vmmd_service", "templates", "99-faas-egress.conf.j2")
	vars := exactPeerVars(map[string]any{
		"cp": map[string]any{"faas_private_address": "10.20.0.1"},
		"c2": map[string]any{"faas_private_dns_address": "10.20.0.3"},
	})
	vars["faas_control_plane_allowed_ports"] = []int{50051, 9091}
	vars["public_iface"] = "eno1"
	vars["masquerade_cidr"] = "10.100.0.0/16"
	vars["ansible_default_ipv4"] = map[string]string{"interface": "eno1"}
	encoded, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json, pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text(), trim_blocks=True).render(**json.loads(sys.argv[2])), end="")
`
	out, err := exec.Command("python3", "-c", script, path, string(encoded)).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v: %s", err, out)
	}
	for _, want := range []string{
		`Environment="FAAS_PRIVATE_INGRESS_CIDRS=10.20.0.0/24"`,
		`Environment="FAAS_PRIVATE_CONTROL_INGRESS_CIDRS=10.20.0.1/32"`,
		`Environment="FAAS_PRIVATE_PEER_INGRESS_CIDRS=10.20.0.9/32,10.20.0.3/32"`,
		`Environment="FAAS_PRIVATE_PEER_INGRESS_TCP_PORTS=50051,9091"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("drop-in lacks %q:\n%s", want, out)
		}
	}
}

// The resolver is copied into both templates; they must not drift.
func TestExactPeerResolverCopiesMatch(t *testing.T) {
	macro := regexp.MustCompile(`(?s)\{% macro faas_exact_cidrs.*?\{%- endmacro %\}`)
	var bodies []string
	for _, rel := range []string{"nftables/templates/policy_nftables.conf.j2", "vmmd_service/templates/99-faas-egress.conf.j2"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", rel))
		if err != nil {
			t.Fatal(err)
		}
		m := macro.Find(raw)
		if m == nil {
			t.Fatalf("%s has no faas_exact_cidrs macro", rel)
		}
		bodies = append(bodies, string(m))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("faas_exact_cidrs differs between the nftables and vmmd templates:\n%s\n---\n%s", bodies[0], bodies[1])
	}
}

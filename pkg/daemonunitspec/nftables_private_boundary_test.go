// adr: 20261109100000 — bare-metal hosts: private services on the private card, sshd bounded.
package daemonunitspec

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func renderHostFirewall(t *testing.T, vars map[string]any) string {
	t.Helper()
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	base := map[string]any{
		"public_iface": "eno1", "tenant_egress_iface": "eno1", "masquerade_cidr": "10.100.0.0/16",
		"faas_postgres_allowed_cidrs": []string{"10.20.0.3/32"}, "faas_compute_allowed_cidrs": []string{"10.20.0.3/32"},
		"faas_compute_allowed_ports": []int{8081}, "faas_control_plane_allowed_cidrs": []string{"10.20.0.2/32"},
		"faas_control_plane_allowed_ports": []int{50051}, "faas_obs_logs_enabled": true,
	}
	for k, v := range vars {
		base[k] = v
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json, pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text(), trim_blocks=True).render(**json.loads(sys.argv[2])), end="")
`
	out, err := exec.Command("python3", "-c", script, hostFirewallTemplate(t), string(encoded)).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v: %s", err, out)
	}
	return string(out)
}

func TestHostFirewallKeepsTheCloudSSHBoundaryByDefault(t *testing.T) {
	for _, mode := range []map[string]any{{}, {"faas_custom_domain_tls": "true"}, {"faas_public_entry_addrs": []string{"203.0.113.7"}}} {
		out := renderHostFirewall(t, mode)
		if !regexp.MustCompile(`(?m)^    tcp dport (22|\{ 22,80,443 \}) accept`).MatchString(out) {
			t.Errorf("mode %v: the cloud sshd rule is gone:\n%s", mode, out)
		}
		if strings.Contains(out, "iifname \"\"") || strings.Contains(out, "sshd from") {
			t.Errorf("mode %v: bare-metal rules leaked into a cloud render:\n%s", mode, out)
		}
	}
}

func TestHostFirewallBoundsSSHAndPrivateServicesOnBareMetal(t *testing.T) {
	for _, mode := range []map[string]any{{}, {"faas_custom_domain_tls": "true"}, {"faas_public_entry_addrs": []string{"203.0.113.7"}}, {"faas_cloudflare_origin_only": true, "faas_cloudflare_ipv4_cidrs": []string{"173.245.48.0/20"}, "faas_cloudflare_ipv6_cidrs": []string{"2400:cb00::/32"}}} {
		vars := map[string]any{
			"faas_private_iface": "eno2", "faas_ssh_restricted": true,
			"faas_ssh_private_cidrs": []string{"10.20.0.0/24"}, "faas_ssh_allowed_cidrs": []string{"198.51.100.10/32"},
		}
		for k, v := range mode {
			vars[k] = v
		}
		out := renderHostFirewall(t, vars)
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "dport 22") || strings.Contains(line, "{ 22,") {
				if !strings.Contains(line, `iifname "eno2" ip saddr 10.20.0.0/24 tcp dport 22 accept`) &&
					!strings.Contains(line, "ip saddr 198.51.100.10/32 tcp dport 22 accept") {
					t.Errorf("mode %v: sshd admitted outside the private network and operator sources: %q", mode, line)
				}
			}
			if strings.Contains(line, `comment "faas `) || strings.Contains(line, `comment "Loki`) || strings.Contains(line, `comment "Promtail`) {
				if !strings.Contains(line, `iifname "eno2" ip saddr`) {
					t.Errorf("mode %v: private service accepted off the private card: %q", mode, line)
				}
			}
		}
		if !strings.Contains(out, `iifname "eno2" ip saddr 10.20.0.0/24 tcp dport 22 accept`) {
			t.Errorf("mode %v: sshd from the private network missing:\n%s", mode, out)
		}
	}
}

package daemonunitspec

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUDPFirewallStringOptInStandardJinja(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	template := filepath.Join("..", "..", "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
	script := `import pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text()).render(public_iface="eth0", masquerade_cidr="10.100.0.0/16", faas_udpd_enabled=sys.argv[2], faas_udpd_allowed_cidrs=["192.0.2.0/24"]), end="")
`
	for _, value := range []string{"false", "False", "0", "no", "off", "true", "True", "1", "yes", "on"} {
		t.Run(value, func(t *testing.T) {
			output, err := exec.Command("python3", "-c", script, template, value).CombinedOutput()
			if err != nil {
				t.Fatalf("standard Jinja render failed: %v: %s", err, output)
			}
			want := value == "true" || value == "True" || value == "1" || value == "yes" || value == "on"
			rule := `ip saddr 192.0.2.0/24 udp dport 40000-49999 accept comment "UDP app listeners"`
			if got := strings.Contains(string(output), rule); got != want {
				t.Fatalf("UDP firewall rule present=%v, want %v for opt-in %q", got, want, value)
			}
		})
	}
}

// ADR-520: customer domains connect to the public edge directly, so a
// Cloudflare origin that enables them must open 80/443 to every source.
// Without them the Cloudflare-only rules are unchanged.
func TestPublicEdgeFirewallCustomDomainsStandardJinja(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	template := filepath.Join("..", "..", "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
	script := `import pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text()).render(public_iface="eth0", masquerade_cidr="10.100.0.0/16",
    faas_cloudflare_origin_only=sys.argv[2] == "true", faas_custom_domain_tls=sys.argv[3],
    faas_cloudflare_ipv4_cidrs=["173.245.48.0/20"], faas_cloudflare_ipv6_cidrs=["2400:cb00::/32"]), end="")
`
	openRule := "tcp dport { 22,80,443 } accept"
	cloudflare := `ip saddr 173.245.48.0/20 tcp dport { 80,443 } accept comment "gateway via Cloudflare"`
	for _, tc := range []struct {
		cloudflareOnly, customDomains string
		wantOpen, wantCloudflare     bool
	}{
		{"true", "false", false, true},
		{"true", "true", true, false},
		{"true", "True", true, false},
		{"false", "false", true, false},
		{"false", "true", true, false},
	} {
		t.Run(tc.cloudflareOnly+"/"+tc.customDomains, func(t *testing.T) {
			output, err := exec.Command("python3", "-c", script, template, tc.cloudflareOnly, tc.customDomains).CombinedOutput()
			if err != nil {
				t.Fatalf("standard Jinja render failed: %v: %s", err, output)
			}
			if got := strings.Contains(string(output), openRule); got != tc.wantOpen {
				t.Errorf("80/443 open to all = %v, want %v", got, tc.wantOpen)
			}
			if got := strings.Contains(string(output), cloudflare); got != tc.wantCloudflare {
				t.Errorf("Cloudflare-only rule = %v, want %v", got, tc.wantCloudflare)
			}
		})
	}
}

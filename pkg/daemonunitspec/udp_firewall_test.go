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

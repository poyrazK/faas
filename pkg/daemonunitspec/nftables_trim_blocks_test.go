package daemonunitspec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The rc.291 control-plane converge (2026-10-24) failed `nft -c` on
// `iif lo accept    iifname "br-tenants" ...`: a `{#- ... #}` comment in the
// host ruleset template, rendered with trim_blocks as ansible.builtin.template
// does, swallowed the newline between two rules. Plain jinja2.Template does
// not trim, so the cross-check passed.

func hostFirewallTemplate(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
}

func TestHostFirewallTemplateHasNoWhitespaceTrimmingComment(t *testing.T) {
	body, err := os.ReadFile(hostFirewallTemplate(t))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	for i, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "{#-") || strings.Contains(line, "-#}") {
			t.Fatalf("policy_nftables.conf.j2:%d: a whitespace-trimming Jinja comment renders differently under Ansible's trim_blocks; put the comment in the role tasks instead: %q", i+1, line)
		}
	}
}

func TestHostFirewallRendersOneRulePerLineUnderAnsibleTrimBlocks(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	script := `import pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text(), trim_blocks=True).render(public_iface="eth0", masquerade_cidr="10.100.0.0/16"), end="")
`
	output, err := exec.Command("python3", "-c", script, hostFirewallTemplate(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("Ansible-style Jinja render failed: %v: %s", err, output)
	}
	lines := strings.Split(string(output), "\n")
	for _, want := range []string{
		`    iif lo accept`,
		`    iifname "br-tenants" ip daddr 10.100.0.0/16 accept`,
		`    iifname "br-tenants" drop`,
	} {
		found := false
		for _, line := range lines {
			if line == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("rendered ruleset has no line %q", want)
		}
	}
	for i, line := range lines {
		if strings.Count(line, " accept") > 1 && !strings.Contains(line, "comment") {
			t.Errorf("rendered line %d holds more than one terminal statement: %q", i+1, line)
		}
	}
}

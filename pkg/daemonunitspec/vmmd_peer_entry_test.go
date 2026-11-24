package daemonunitspec

// adr: 20261123100000 — vmmd rebuilds the compute node's whole ruleset, so
// its drop-in must hand it the other compute edges' entry addresses, or the
// first rebuild drops the hairpin of guests' calls to them.

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVmmdDropinCarriesPeerEntryAddrs(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
print(Environment(trim_blocks=True).from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])))
`
	tmpl := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "vmmd_service", "templates", "99-faas-egress.conf.j2")
	for _, tc := range []struct {
		name  string
		peers []string
		want  string
	}{
		{name: "peers", peers: []string{"198.51.100.3", "198.51.100.4"}, want: `Environment="FAAS_PEER_ENTRY_ADDRS=198.51.100.3,198.51.100.4"`},
		{name: "no peers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := json.Marshal(map[string]any{
				"ansible_default_ipv4":             map[string]string{"interface": "eth0"},
				"public_iface":                     "eth0",
				"masquerade_cidr":                  "10.100.0.0/16",
				"faas_control_plane_allowed_cidrs": []string{"10.42.0.1/32"},
				"faas_control_plane_allowed_ports": []int{50051},
				"faas_public_entry_addrs":          []string{"198.51.100.2"},
				"faas_peer_entry_addrs":            tc.peers,
			})
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("python3", "-c", script, tmpl, string(values)).CombinedOutput()
			if err != nil {
				t.Fatalf("render: %v: %s", err, out)
			}
			got := ""
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, `Environment="FAAS_PEER_ENTRY_ADDRS=`) {
					got = line
				}
			}
			if got != tc.want {
				t.Fatalf("FAAS_PEER_ENTRY_ADDRS line = %q, want %q", got, tc.want)
			}
		})
	}
}

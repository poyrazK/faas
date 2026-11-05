package daemonunitspec

// adr: 20261102081618 — vmmd rebuilds the compute node's whole ruleset, so
// its drop-in must hand it the same Cloudflare ranges the boot policy admits
// on 80/443, or the first rebuild reopens them to everyone. An edge with no
// proxy in front (ADR-20261103163954) hands it its entry addresses instead.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVmmdPublicIngressFollowsCloudflareOriginOnly(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	var families [2][]string
	for i, name := range []string{"cloudflare-ips-v4.txt", "cloudflare-ips-v6.txt"} {
		raw, err := os.ReadFile(filepath.Join(roles, "nftables", "files", name))
		if err != nil {
			t.Fatal(err)
		}
		families[i] = strings.Fields(string(raw))
	}
	ranges := append(append([]string(nil), families[0]...), families[1]...)
	script := `import json, pathlib, sys
from jinja2 import Environment
print(Environment(trim_blocks=True).from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])))
`
	for _, tc := range []struct {
		name            string
		on              bool
		entry           []string
		want, entryWant string
	}{
		{name: "Cloudflare-only origin", on: true, want: `Environment="FAAS_PUBLIC_INGRESS_CIDRS=` + strings.Join(ranges, ",") + `"`},
		{name: "open origin", on: false},
		{name: "no proxy in front", on: true, entry: []string{"203.0.113.10", "2001:db8::10"},
			want:      `Environment="FAAS_PUBLIC_INGRESS_CIDRS=` + strings.Join(ranges, ",") + `"`,
			entryWant: `Environment="FAAS_PUBLIC_ENTRY_ADDRS=203.0.113.10,2001:db8::10"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := json.Marshal(map[string]any{
				"ansible_default_ipv4":             map[string]string{"interface": "eth0"},
				"public_iface":                     "eth0",
				"masquerade_cidr":                  "10.100.0.0/16",
				"faas_control_plane_allowed_cidrs": []string{"10.42.0.1/32"},
				"faas_control_plane_allowed_ports": []int{50051},
				"faas_cloudflare_origin_only":      tc.on,
				"faas_cloudflare_ipv4_cidrs":       families[0],
				"faas_cloudflare_ipv6_cidrs":       families[1],
				"faas_public_entry_addrs":          tc.entry,
			})
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("python3", "-c", script,
				filepath.Join(roles, "vmmd_service", "templates", "99-faas-egress.conf.j2"), string(values)).CombinedOutput()
			if err != nil {
				t.Fatalf("render: %v: %s", err, out)
			}
			got, entryGot := "", ""
			for _, line := range strings.Split(string(out), "\n") {
				switch {
				case strings.HasPrefix(line, `Environment="FAAS_PUBLIC_INGRESS_CIDRS=`):
					got = line
				case strings.HasPrefix(line, `Environment="FAAS_PUBLIC_ENTRY_ADDRS=`):
					entryGot = line
				}
			}
			if got != tc.want {
				t.Fatalf("FAAS_PUBLIC_INGRESS_CIDRS line = %q, want %q", got, tc.want)
			}
			if entryGot != tc.entryWant {
				t.Fatalf("FAAS_PUBLIC_ENTRY_ADDRS line = %q, want %q", entryGot, tc.entryWant)
			}
		})
	}
}

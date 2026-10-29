package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// vmmd replaces the compute node's whole ruleset whenever it re-renders the
// host policy, keeping only the control-plane ports its drop-in lists. On
// rc.304 (2026-10-29) that dropped the app edge's control and connector ports
// the boot policy had opened, so the control plane could neither probe nor
// scrape either edge. The drop-in must list exactly the ports the boot policy
// opens to the control plane, whichever features are on.
func TestVmmdPrivateIngressMatchesBootPolicy(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	root := repoRoot(t)
	roles := filepath.Join(root, "deploy", "ansible", "roles")
	raw, err := os.ReadFile(filepath.Join(roles, "nftables", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var defaults map[string]any
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
env = Environment(trim_blocks=True)
values = json.loads(sys.argv[3])
for path in sys.argv[1:3]:
    print(env.from_string(pathlib.Path(path).read_text()).render(**values))
    print("--- end of template ---")
`
	const controlPlane = "10.42.0.1/32"
	bootRule := regexp.MustCompile(`ip saddr ` + regexp.QuoteMeta(controlPlane) + ` tcp dport (\{[^}]*\}|\d+) accept`)
	vmmdPorts := regexp.MustCompile(`FAAS_PRIVATE_INGRESS_TCP_PORTS=([0-9,]*)"`)
	for _, tc := range []struct {
		name      string
		edge, obs bool
		want      []int
	}{
		{name: "fixed list"},
		{name: "app edge", edge: true, want: []int{9109, 20241}},
		{name: "platform logs", obs: true, want: []int{9080}},
		{name: "both", edge: true, obs: true, want: []int{9080, 9109, 20241}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]any{}
			for k, v := range defaults {
				values[k] = v
			}
			values["public_iface"] = "eth0"
			values["masquerade_cidr"] = "10.100.0.0/16"
			values["ansible_default_ipv4"] = map[string]string{"interface": "eth0"}
			values["faas_control_plane_allowed_cidrs"] = []string{controlPlane}
			values["faas_edge_on_compute_nodes"] = tc.edge
			values["faas_obs_logs_enabled"] = tc.obs
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("python3", "-c", script,
				filepath.Join(roles, "nftables", "templates", "policy_nftables.conf.j2"),
				filepath.Join(roles, "vmmd_service", "templates", "99-faas-egress.conf.j2"),
				string(encoded)).CombinedOutput()
			if err != nil {
				t.Fatalf("render: %v: %s", err, out)
			}
			parts := strings.Split(string(out), "--- end of template ---")
			if len(parts) != 3 {
				t.Fatalf("render printed %d templates, want 2: %s", len(parts)-1, out)
			}

			var boot []int
			for _, m := range bootRule.FindAllStringSubmatch(parts[0], -1) {
				boot = append(boot, parsePortList(t, strings.Trim(m[1], "{} "))...)
			}
			m := vmmdPorts.FindStringSubmatch(parts[1])
			if m == nil {
				t.Fatalf("drop-in has no FAAS_PRIVATE_INGRESS_TCP_PORTS:\n%s", parts[1])
			}
			vmmd := parsePortList(t, m[1])
			slices.Sort(boot)
			slices.Sort(vmmd)
			if !slices.Equal(boot, vmmd) {
				t.Fatalf("vmmd opens %v to the control plane, the boot policy %v", vmmd, boot)
			}
			for _, port := range tc.want {
				if !slices.Contains(vmmd, port) {
					t.Errorf("vmmd's ports %v lack %d", vmmd, port)
				}
			}
		})
	}
}

func parsePortList(t *testing.T, list string) []int {
	t.Helper()
	var ports []int
	for _, field := range strings.Split(list, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		port, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("port %q: %v", field, err)
		}
		ports = append(ports, port)
	}
	return ports
}

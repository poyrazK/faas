// adr: 1343 — the app edge on compute nodes: unit, role and firewall contract.
package daemonunitspec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The edge is gatewayd-public with a compute node's dependencies and
// listeners. Everything else, the hardening above all, must stay identical
// to the control-plane unit it is derived from.
func TestUnitGatewaydPublicEdgeMatchesControlPlaneHardening(t *testing.T) {
	edge, public := UnitGatewaydPublicEdge(), UnitGatewaydPublic()
	for _, deps := range [][]string{edge.After, edge.Wants, edge.Requires} {
		for _, dep := range deps {
			if dep == "postgresql.service" || dep == "faas-apid.service" {
				t.Errorf("edge depends on %s, which a compute node does not run", dep)
			}
		}
	}
	env := map[string]string{}
	for _, kv := range edge.Environment {
		env[kv.Key] = kv.Value
	}
	for key, want := range map[string]string{
		"FAAS_PUBLIC_EDGE_MODE":          "compute",
		"FAAS_GATEWAYD_PUBLIC_ROLE":      "compute-only",
		"FAAS_PUBLIC_LISTEN_ADDR":        "127.0.0.1:8090",
		"FAAS_TRUSTED_INGRESS_CIDRS":     "127.0.0.0/8,::1/128",
		"FAAS_OTEL_SPANS_WRITER_ENABLED": "false",
	} {
		if env[key] != want {
			t.Errorf("edge %s = %q, want %q", key, env[key], want)
		}
	}
	for _, key := range []string{"FAAS_OBJECT_STORAGE_CONFIG", "FAAS_INTERNAL_TARGET", "FAAS_COMPUTE_GATEWAY_DISCOVERY"} {
		if _, ok := env[key]; ok {
			t.Errorf("edge sets %s, which only the control-plane edge reads", key)
		}
	}
	if strings.Contains(edge.EnvironmentFile, "object-storage") {
		t.Errorf("edge loads object-storage credentials: %s", edge.EnvironmentFile)
	}

	// Blank the fields the edge overrides; the rest must be equal.
	edge.Description, public.Description = "", ""
	edge.After, public.After = nil, nil
	edge.Wants, public.Wants = nil, nil
	edge.Requires, public.Requires = nil, nil
	edge.EnvironmentFile, public.EnvironmentFile = "", ""
	edge.Environment, public.Environment = nil, nil
	if !reflect.DeepEqual(edge, public) {
		t.Errorf("edge unit drifted from gatewayd-public beyond its compute-node overrides:\nedge   %+v\npublic %+v", edge, public)
	}
}

// The socket, the unit's declared listener and the registry probe name the
// same address, which must not be gatewayd-internal's 8080 on the same node.
func TestGatewaydPublicEdgeSocketMatchesUnitAndProbe(t *testing.T) {
	root := repoRoot(t)
	socket, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_public_edge_service", "files", "faas-gatewayd-public-edge.socket"))
	if err != nil {
		t.Fatal(err)
	}
	listen := regexp.MustCompile(`(?m)^ListenStream=(\S+)$`).FindStringSubmatch(string(socket))
	if listen == nil {
		t.Fatal("edge socket has no ListenStream")
	}
	if !strings.Contains(string(socket), "FileDescriptorName=public\n") {
		t.Error("edge socket must hand gatewayd-public the fd it looks up by name (public)")
	}
	var unitListen string
	for _, kv := range UnitGatewaydPublicEdge().Environment {
		if kv.Key == "FAAS_PUBLIC_LISTEN_ADDR" {
			unitListen = kv.Value
		}
	}
	var probe string
	for _, entry := range OptionalRegistry {
		if entry.Name == "gatewayd-public-edge" {
			probe = entry.Lifecycle.ProbeTarget
			if entry.Role != RoleComputeOnly {
				t.Errorf("gatewayd-public-edge role = %s, want %s", entry.Role, RoleComputeOnly)
			}
		}
	}
	if listen[1] != unitListen || listen[1] != probe {
		t.Errorf("socket %s, unit %s and probe %s disagree", listen[1], unitListen, probe)
	}
	if strings.HasSuffix(listen[1], ":8080") {
		t.Error("edge socket collides with gatewayd-internal's 8080 on a compute node")
	}
}

func TestGatewaydPublicEdgeRoleContract(t *testing.T) {
	root := repoRoot(t)
	roleDir := filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_public_edge_service")
	read := func(rel string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(roleDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	var defaults struct {
		Enabled bool              `yaml:"faas_edge_on_compute_nodes"`
		Version string            `yaml:"faas_cloudflared_version"`
		SHA256  map[string]string `yaml:"faas_cloudflared_sha256"`
		Token   string            `yaml:"faas_cloudflared_token_path"`
	}
	if err := yaml.Unmarshal([]byte(read("defaults/main.yml")), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Enabled {
		t.Error("the edge must default off; the manifest turns it on per cell")
	}
	// --token-file arrived in cloudflared 2025.4.0.
	var year, month, patch int
	if n, err := fmt.Sscanf(defaults.Version, "%d.%d.%d", &year, &month, &patch); err != nil || n != 3 || year*100+month < 202504 {
		t.Errorf("cloudflared version %q is not a pinned release with --token-file", defaults.Version)
	}
	sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, arch := range []string{"amd64", "arm64"} {
		if !sha.MatchString(defaults.SHA256[arch]) {
			t.Errorf("cloudflared %s checksum %q is not a SHA-256", arch, defaults.SHA256[arch])
		}
	}

	main := flattenRoleTasks("main.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "main.yml")))
	gates := map[string]string{}
	for _, task := range main {
		if file, ok := task.body["ansible.builtin.include_tasks"].(string); ok {
			gates[file], _ = task.body["when"].(string)
		}
	}
	if gates["enable.yml"] != "faas_edge_on_compute_nodes | bool" || gates["disable.yml"] != "not (faas_edge_on_compute_nodes | bool)" {
		t.Errorf("enable/disable are not gated on the manifest switch: %v", gates)
	}

	installed := map[string]bool{}
	var checksum string
	var contents []string
	for _, task := range flattenRoleTasks("enable.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "enable.yml"))) {
		if args, ok := task.body["ansible.builtin.get_url"].(map[string]any); ok {
			checksum, _ = args["checksum"].(string)
		}
		for _, module := range []string{"ansible.builtin.copy", "ansible.builtin.template"} {
			args, ok := task.body[module].(map[string]any)
			if !ok {
				continue
			}
			dest, _ := args["dest"].(string)
			if strings.Contains(dest, "{{ item }}") {
				for _, item := range task.body["loop"].([]any) {
					installed[strings.ReplaceAll(dest, "{{ item }}", item.(string))] = true
				}
			} else if strings.HasPrefix(dest, "/etc/systemd/system/") {
				installed[dest] = true
			}
			if content, ok := args["content"].(string); ok {
				contents = append(contents, content)
			}
		}
		if args, ok := task.body["ansible.builtin.systemd_service"].(map[string]any); ok && args["state"] == "started" {
			if when, _ := task.body["when"].(string); !strings.Contains(when, "faas_join_defer_service_handlers") {
				t.Error("the edge starts during a node join that defers service starts")
			}
		}
	}
	if checksum != "sha256:{{ faas_cloudflared_sha256[faas_cloudflared_arch] }}" {
		t.Errorf("connector download is not verified against the pinned checksum: %q", checksum)
	}
	joined := strings.Join(contents, "\n")
	for _, want := range []string{"PartOf=faas-gatewayd-internal.service", "Wants=faas-gatewayd-public-edge.service"} {
		if !strings.Contains(joined, want) {
			t.Errorf("edge is not tied to gatewayd-internal's lifecycle: missing %s", want)
		}
	}

	// Everything enable installs under /etc/systemd/system, disable removes:
	// a unit file directly, a drop-in through its unit's .d directory.
	var removed []string
	for _, task := range flattenRoleTasks("disable.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "disable.yml"))) {
		if args, ok := task.body["ansible.builtin.file"].(map[string]any); ok && args["state"] == "absent" {
			for _, item := range task.body["loop"].([]any) {
				removed = append(removed, item.(string))
			}
		}
	}
	for path := range installed {
		ok := false
		for _, gone := range removed {
			if path == gone || strings.HasPrefix(path, gone+"/") {
				ok = true
			}
		}
		if !ok {
			t.Errorf("disable leaves %s behind", path)
		}
	}

	connector := read("templates/faas-cloudflared.service.j2")
	for _, want := range []string{
		"ConditionPathExists={{ faas_cloudflared_token_path }}",
		"LoadCredential=tunnel-token:{{ faas_cloudflared_token_path }}",
		"--token-file %d/tunnel-token",
		"--no-autoupdate",
		"User=cloudflared",
		"NoNewPrivileges=yes",
	} {
		if !strings.Contains(connector, want) {
			t.Errorf("connector unit missing %q", want)
		}
	}
	if strings.Contains(connector, "--token ") {
		t.Error("connector passes the tunnel token on the command line")
	}

	playbook, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	computePlay := string(playbook)[strings.Index(string(playbook), "- name: Bootstrap a Gate-B compute-only node"):]
	internal := strings.Index(computePlay, "    - role: gatewayd_internal_service\n")
	edge := strings.Index(computePlay, "    - role: gatewayd_public_edge_service\n")
	if internal < 0 || edge < internal {
		t.Error("compute play must run gatewayd_public_edge_service after gatewayd_internal_service")
	}
	if strings.Count(string(playbook), "role: gatewayd_public_edge_service") != 1 {
		t.Error("gatewayd_public_edge_service must run only in the compute play")
	}
}

// The control plane's private listener and the compute peers' fallback port
// open only while the manifest runs the edge on compute nodes.
func TestComputeEdgeFirewallStandardJinja(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	root := repoRoot(t)
	template := filepath.Join(root, "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
	script := `import pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text()).render(public_iface="eth0", masquerade_cidr="10.100.0.0/16",
    faas_edge_on_compute_nodes=sys.argv[2], faas_edge_private_listen_addr="10.42.0.1:8090",
    faas_compute_allowed_cidrs=["10.42.0.2/32"], faas_compute_allowed_ports=[9091],
    faas_edge_peer_cidrs=["10.42.0.2/32", "10.42.0.3/32"], faas_edge_peer_gateway_port=8080), end="")
`
	private := `ip saddr 10.42.0.2/32 tcp dport 8090 accept comment "faas app edge forwards from compute (ADR-1343)"`
	peer := `ip saddr 10.42.0.3/32 tcp dport 8080 accept comment "faas app edge compute-pool fallback (ADR-1343)"`
	for _, tc := range []struct {
		enabled string
		want    bool
	}{{"true", true}, {"True", true}, {"false", false}} {
		t.Run(tc.enabled, func(t *testing.T) {
			output, err := exec.Command("python3", "-c", script, template, tc.enabled).CombinedOutput()
			if err != nil {
				t.Fatalf("standard Jinja render failed: %v: %s", err, output)
			}
			for _, rule := range []string{private, peer} {
				if got := strings.Contains(string(output), rule); got != tc.want {
					t.Errorf("rule %q present = %v, want %v", rule, got, tc.want)
				}
			}
		})
	}

	cp, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_public_service", "templates", "99-faas-edge-private.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	dropIn := `import pathlib, sys
from jinja2 import Template
print(Template(sys.argv[1]).render(faas_edge_private_listen_addr="10.42.0.1:8090", faas_compute_allowed_cidrs=["10.42.0.2/32"]), end="")
`
	output, err := exec.Command("python3", "-c", dropIn, string(cp)).CombinedOutput()
	if err != nil {
		t.Fatalf("standard Jinja render failed: %v: %s", err, output)
	}
	for _, want := range []string{
		"Environment=FAAS_PUBLIC_PRIVATE_LISTEN_ADDR=10.42.0.1:8090\n",
		"Environment=FAAS_TRUSTED_INGRESS_CIDRS=127.0.0.0/8,::1/128,10.42.0.2/32\n",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("control-plane edge drop-in missing %q:\n%s", want, output)
		}
	}
}

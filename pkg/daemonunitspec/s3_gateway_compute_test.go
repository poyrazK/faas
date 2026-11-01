// adr: 20261031122757 — the S3 gateway on compute nodes: listeners, s3 tunnel
// connector, firewall and scrape.
package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The gateway's control listener and its connector's metrics bind every
// address on a compute node, so neither port may be one another listener
// there holds, the app edge's included. The data listener stays on loopback.
func TestComputeObjectGatewayPortsAreFreeOnComputeNodes(t *testing.T) {
	taken := map[string]string{ComputeEdgeControlPort: "the app edge's control listener"}
	for port, owner := range computeNodeListeners {
		taken[port] = owner
	}
	for _, port := range []string{ComputeObjectGatewayControlPort, ComputeObjectGatewayConnectorMetricsPort} {
		if owner, ok := taken[port]; ok {
			t.Errorf("port %s is held by %s on a compute node", port, owner)
		}
	}
	if ComputeObjectGatewayControlPort == ComputeObjectGatewayConnectorMetricsPort {
		t.Error("the gateway and its connector share a port")
	}
	env := map[string]string{}
	for _, kv := range UnitS3GatewayCompute().Environment {
		env[kv.Key] = kv.Value
	}
	if want := "0.0.0.0:" + ComputeObjectGatewayControlPort; env["FAAS_S3_GATEWAY_CONTROL_ADDR"] != want {
		t.Errorf("FAAS_S3_GATEWAY_CONTROL_ADDR = %q, want %q", env["FAAS_S3_GATEWAY_CONTROL_ADDR"], want)
	}
	if env["FAAS_S3_GATEWAY_LISTEN_ADDR"] != "127.0.0.1:8084" {
		t.Errorf("FAAS_S3_GATEWAY_LISTEN_ADDR = %q; only the node's connector may reach the data listener", env["FAAS_S3_GATEWAY_LISTEN_ADDR"])
	}
	for _, d := range OptionalRegistry {
		if d.Name == "s3-gatewayd-compute" && d.Lifecycle.ReadyzURL != "http://127.0.0.1:"+ComputeObjectGatewayControlPort+"/readyz" {
			t.Errorf("gateway readiness probe %q does not use its control port", d.Lifecycle.ReadyzURL)
		}
	}
}

// The s3 tunnel connector follows the gateway: it starts after the gateway
// is ready, leaves the tunnel when the gateway stops, drains through it on a
// stop, and keeps the app connector's hardening and token handling.
func TestComputeObjectGatewayConnectorContract(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	read := func(parts ...string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(append([]string{roles}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	connector := read("s3_gateway_compute_service", "templates", "faas-cloudflared-s3.service.j2")
	for _, want := range []string{
		"After=network-online.target faas-s3-gatewayd-compute.service",
		"BindsTo=faas-s3-gatewayd-compute.service",
		"PartOf=faas-s3-gatewayd-compute.service",
		"WantedBy=faas-s3-gatewayd-compute.service",
		"ConditionPathExists={{ faas_cloudflared_s3_token_path }}",
		"LoadCredential=tunnel-token:{{ faas_cloudflared_s3_token_path }}",
		"ExecStart={{ faas_cloudflared_install_dir }}/cloudflared ",
		"--metrics {{ faas_cloudflared_s3_metrics_addr }}",
		"--grace-period {{ faas_cloudflared_s3_grace_period }}",
		"TimeoutStopSec={{ faas_cloudflared_s3_stop_timeout }}",
	} {
		if !strings.Contains(connector, want) {
			t.Errorf("s3 connector unit missing %q", want)
		}
	}
	// Apart from its own variables and lifecycle, the [Service] section is
	// the app connector's.
	service := func(unit string) string {
		return unit[strings.Index(unit, "[Service]"):strings.Index(unit, "[Install]")]
	}
	app := service(read("gatewayd_public_edge_service", "templates", "faas-cloudflared.service.j2"))
	s3 := strings.ReplaceAll(service(connector), "faas_cloudflared_s3_", "faas_cloudflared_")
	if app != s3 {
		t.Errorf("s3 connector [Service] drifted from the app connector's:\n%s\nwant:\n%s", s3, app)
	}

	var defaults struct {
		Token   string `yaml:"faas_cloudflared_s3_token_path"`
		Metrics string `yaml:"faas_cloudflared_s3_metrics_addr"`
		Grace   string `yaml:"faas_cloudflared_s3_grace_period"`
		Stop    string `yaml:"faas_cloudflared_s3_stop_timeout"`
	}
	if err := yaml.Unmarshal([]byte(read("s3_gateway_compute_service", "defaults", "main.yml")), &defaults); err != nil {
		t.Fatal(err)
	}
	var edge struct {
		Token string `yaml:"faas_cloudflared_token_path"`
	}
	if err := yaml.Unmarshal([]byte(read("gatewayd_public_edge_service", "defaults", "main.yml")), &edge); err != nil {
		t.Fatal(err)
	}
	if defaults.Token == "" || defaults.Token == edge.Token || filepath.Dir(defaults.Token) != filepath.Dir(edge.Token) {
		t.Errorf("s3 tunnel token %q must be its own file beside the app tunnel's %q", defaults.Token, edge.Token)
	}
	if want := "0.0.0.0:" + ComputeObjectGatewayConnectorMetricsPort; defaults.Metrics != want {
		t.Errorf("s3 connector metrics on %q, want %q", defaults.Metrics, want)
	}
	grace, graceErr := time.ParseDuration(defaults.Grace)
	stop, stopErr := time.ParseDuration(defaults.Stop)
	if graceErr != nil || stopErr != nil || stop <= grace {
		t.Errorf("s3 connector grace %q / stop timeout %q: systemd must outwait the drain", defaults.Grace, defaults.Stop)
	}

	roleDir := filepath.Join(roles, "s3_gateway_compute_service")
	installed := false
	enabled := map[string]bool{}
	for _, task := range flattenRoleTasks("enable.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "enable.yml"))) {
		if args, ok := task.body["ansible.builtin.template"].(map[string]any); ok && args["dest"] == "/etc/systemd/system/faas-cloudflared-s3.service" {
			installed = true
		}
		if args, ok := task.body["ansible.builtin.systemd_service"].(map[string]any); ok && args["enabled"] == true {
			for _, item := range task.body["loop"].([]any) {
				enabled[item.(string)] = true
			}
		}
	}
	if !installed || !enabled["faas-cloudflared-s3.service"] {
		t.Errorf("enable must install and enable faas-cloudflared-s3 (installed %v, enabled %v)", installed, enabled)
	}
	removed := false
	for _, task := range flattenRoleTasks("disable.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "disable.yml"))) {
		if args, ok := task.body["ansible.builtin.file"].(map[string]any); ok && args["state"] == "absent" {
			for _, item := range task.body["loop"].([]any) {
				removed = removed || item == "/etc/systemd/system/faas-cloudflared-s3.service"
			}
		}
	}
	if !removed {
		t.Error("disable leaves faas-cloudflared-s3.service behind")
	}
}

// The control plane scrapes the gateways and their connectors, and compute
// nodes admit it to both ports, only while the manifest runs the gateway on
// compute nodes.
func TestComputeObjectGatewayScrapeAndFirewallFollowTheSwitch(t *testing.T) {
	root := repoRoot(t)
	read := func(parts ...string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(append([]string{root, "deploy", "ansible", "roles"}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	tmpl := read("prometheus", "templates", "prometheus.yml.j2")
	gate := strings.Index(tmpl, "{% if faas_object_gateway_on_compute_nodes | default(false) | bool %}")
	if gate < 0 {
		t.Fatal("prometheus.yml.j2 does not gate the compute gateway jobs on faas_object_gateway_on_compute_nodes")
	}
	end := gate + strings.Index(tmpl[gate:], "{% endif %}")
	for _, job := range []string{`job_name: "s3-gatewayd-compute"`, `job_name: "cloudflared-s3"`} {
		at := strings.Index(tmpl, job)
		if at < gate || at > end {
			t.Errorf("%s is not inside the compute gateway switch", job)
		}
	}
	defaults := read("prometheus", "defaults", "main.yml")
	for _, want := range []string{"/v1/internal/metrics/s3-gatewayd-compute-targets", "/v1/internal/metrics/cloudflared-s3-targets"} {
		if !strings.Contains(defaults, want) {
			t.Errorf("prometheus defaults missing %s", want)
		}
	}
	nft := read("nftables", "defaults", "main.yml")
	for _, want := range []string{
		"faas_object_gateway_control_port: " + ComputeObjectGatewayControlPort + "\n",
		"faas_object_gateway_connector_metrics_port: " + ComputeObjectGatewayConnectorMetricsPort + "\n",
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("nftables defaults missing %q", want)
		}
	}

	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for firewall rendering")
	}
	template := filepath.Join(root, "deploy", "ansible", "roles", "nftables", "templates", "policy_nftables.conf.j2")
	script := `import pathlib, sys
from jinja2 import Template
print(Template(pathlib.Path(sys.argv[1]).read_text()).render(public_iface="eth0", masquerade_cidr="10.100.0.0/16",
    faas_object_gateway_on_compute_nodes=sys.argv[2],
    faas_control_plane_allowed_cidrs=["10.42.0.1/32"], faas_control_plane_allowed_ports=[9092],
    faas_object_gateway_control_port=9096, faas_object_gateway_connector_metrics_port=20242), end="")
`
	control := `ip saddr 10.42.0.1/32 tcp dport 9096 accept comment "faas s3 gateway control listener from control (ADR-20261031122757)"`
	connector := `ip saddr 10.42.0.1/32 tcp dport 20242 accept comment "faas s3 tunnel connector metrics from control (ADR-20261031122757)"`
	for _, tc := range []struct {
		enabled string
		want    bool
	}{{"true", true}, {"True", true}, {"false", false}} {
		t.Run(tc.enabled, func(t *testing.T) {
			output, err := exec.Command("python3", "-c", script, template, tc.enabled).CombinedOutput()
			if err != nil {
				t.Fatalf("standard Jinja render failed: %v: %s", err, output)
			}
			for _, rule := range []string{control, connector} {
				if got := strings.Contains(string(output), rule); got != tc.want {
					t.Errorf("rule %q present = %v, want %v", rule, got, tc.want)
				}
			}
		})
	}
}

// The load test's pilot endpoint replaces the registry's public_endpoint only
// while it is set; unset, the node keeps the control plane's endpoint, so the
// cutover needs nothing but the variable cleared.
func TestComputeObjectGatewayPilotEndpoint(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "s3_gateway_compute_service")
	var defaults struct {
		Endpoint *string `yaml:"faas_object_gateway_public_endpoint"`
	}
	raw, err := os.ReadFile(filepath.Join(roleDir, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Endpoint == nil || *defaults.Endpoint != "" {
		t.Fatal("faas_object_gateway_public_endpoint must default to '' (the control plane's endpoint)")
	}
	var content string
	for _, task := range flattenRoleTasks("enable.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "enable.yml"))) {
		if args, ok := task.body["ansible.builtin.copy"].(map[string]any); ok && args["dest"] == "/etc/faas/object-storage.json" {
			content, _ = args["content"].(string)
		}
	}
	if !strings.Contains(content, "faas_object_gateway_public_endpoint") {
		t.Fatalf("the node's registry ignores the pilot endpoint:\n%s", content)
	}
	for _, tc := range []struct {
		endpoint, want string
	}{{"", "https://s3.gregale.dev"}, {"https://s3-pilot.gregale.dev", "https://s3-pilot.gregale.dev"}} {
		rendered := renderPublicEdgeBlock(t, content, map[string]any{
			"faas_object_gateway_registry":             map[string]any{"public_endpoint": "https://s3.gregale.dev", "transfer": map[string]any{"profile": "proxied"}},
			"faas_object_gateway_max_bytes_per_second": 37500000,
			"faas_object_gateway_public_endpoint":      tc.endpoint,
		})
		var registry struct {
			PublicEndpoint string `json:"public_endpoint"`
			Transfer       struct {
				MaxBytesPerSecond int64 `json:"max_bytes_per_second"`
			} `json:"transfer"`
		}
		if err := json.Unmarshal([]byte(rendered), &registry); err != nil {
			t.Fatalf("rendered registry is not JSON: %v\n%s", err, rendered)
		}
		if registry.PublicEndpoint != tc.want || registry.Transfer.MaxBytesPerSecond != 37500000 {
			t.Errorf("pilot endpoint %q rendered public_endpoint %q, max_bytes_per_second %d", tc.endpoint, registry.PublicEndpoint, registry.Transfer.MaxBytesPerSecond)
		}
	}
}

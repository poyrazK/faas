// adr: 20261102081618
package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The edge's direct entry is off unless the manifest sets public_edge.entry:
// both, which renders the TLS listener's settings, its credentials and the
// port-binding capability into the edge drop-in; the role refuses to point the
// edge at TLS material the node does not have.
func TestGatewaydPublicEdgeDirectEntry(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_public_edge_service")
	read := func(rel string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(roleDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	defaults := map[string]any{}
	if err := yaml.Unmarshal([]byte(read("defaults/main.yml")), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults["faas_edge_entry"] != "tunnel" {
		t.Errorf("faas_edge_entry defaults to %v, want tunnel; the manifest turns direct entry on per cell", defaults["faas_edge_entry"])
	}
	if enable := read("tasks/enable.yml"); !strings.Contains(enable, "require the direct-entry TLS material") {
		t.Error("the role must check the TLS material before it renders direct entry")
	}

	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
print(Environment(trim_blocks=True).from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	want := []string{
		"AmbientCapabilities=CAP_NET_BIND_SERVICE\n",
		"LoadCredential=edge_tls_cert:/etc/faas/secrets/edge-tls/origin.pem\n",
		"LoadCredential=edge_tls_key:/etc/faas/secrets/edge-tls/origin.key\n",
		"LoadCredential=edge_tls_client_ca:/etc/faas/secrets/edge-tls/origin-pull-ca.pem\n",
		"Environment=FAAS_EDGE_PUBLIC_TLS_ADDR=:443\n",
		"Environment=FAAS_EDGE_PUBLIC_TLS_CERT_FILE=%d/edge_tls_cert\n",
		"Environment=FAAS_EDGE_PUBLIC_TLS_KEY_FILE=%d/edge_tls_key\n",
		"Environment=FAAS_EDGE_PUBLIC_TLS_CLIENT_CA_FILE=%d/edge_tls_client_ca\n",
	}
	for _, entry := range []string{"tunnel", "both", ""} {
		t.Run("entry="+entry, func(t *testing.T) {
			values := map[string]any{"faas_edge_control_plane_target": "http://10.42.0.1:8090"}
			for k, v := range defaults {
				values[k] = v
			}
			if entry == "" {
				delete(values, "faas_edge_entry")
			} else {
				values["faas_edge_entry"] = entry
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("python3", "-c", script, filepath.Join(roleDir, "templates", "99-faas-edge.conf.j2"), string(encoded)).CombinedOutput()
			if err != nil {
				t.Fatalf("standard Jinja render failed: %v: %s", err, output)
			}
			for _, line := range want {
				if got := strings.Contains(string(output), line); got != (entry == "both") {
					t.Errorf("%q rendered = %v, want %v:\n%s", line, got, entry == "both", output)
				}
			}
		})
	}
}

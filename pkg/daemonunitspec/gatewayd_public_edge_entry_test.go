// adr: 20261102081618, 20261103163954
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
	if defaults["faas_edge_public_front"] != "cloudflare" {
		t.Errorf("faas_edge_public_front defaults to %v, want cloudflare", defaults["faas_edge_public_front"])
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
	render := func(t *testing.T, overrides map[string]any) string {
		t.Helper()
		values := map[string]any{"faas_edge_control_plane_target": "http://10.42.0.1:8090"}
		for k, v := range defaults {
			values[k] = v
		}
		for k, v := range overrides {
			if v == nil {
				delete(values, k)
			} else {
				values[k] = v
			}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("python3", "-c", script, filepath.Join(roleDir, "templates", "99-faas-edge.conf.j2"), string(encoded)).CombinedOutput()
		if err != nil {
			t.Fatalf("standard Jinja render failed: %v: %s", err, output)
		}
		return string(output)
	}
	const noFront = "Environment=FAAS_EDGE_PUBLIC_FRONT=none\n"
	for _, entry := range []string{"tunnel", "both", ""} {
		t.Run("entry="+entry, func(t *testing.T) {
			var value any = entry
			if entry == "" {
				value = nil
			}
			output := render(t, map[string]any{"faas_edge_entry": value})
			for _, line := range want {
				if got := strings.Contains(output, line); got != (entry == "both") {
					t.Errorf("%q rendered = %v, want %v:\n%s", line, got, entry == "both", output)
				}
			}
			if strings.Contains(output, noFront) {
				t.Errorf("the default front rendered %q:\n%s", noFront, output)
			}
		})
	}
	// ADR-20261103163954 §2: with no front the edge serves clients directly
	// and still verifies the zone's client certificate when given, so it
	// keeps every setting and gains the front switch.
	t.Run("entry=both front=none", func(t *testing.T) {
		output := render(t, map[string]any{"faas_edge_entry": "both", "faas_edge_public_front": "none"})
		for _, line := range append(want, noFront) {
			if !strings.Contains(output, line) {
				t.Errorf("%q not rendered:\n%s", line, output)
			}
		}
		if strings.Contains(output, "FAAS_EDGE_PROXY_CIDRS") {
			t.Errorf("Cloudflare ranges rendered for a node that keeps none:\n%s", output)
		}
	})
	// ADR-20261103163954 §4: a node that keeps Cloudflare's ranges hands them
	// to the edge, whose connection limits skip them for the flood fallback.
	t.Run("entry=both front=none cloudflare kept", func(t *testing.T) {
		output := render(t, map[string]any{
			"faas_edge_entry": "both", "faas_edge_public_front": "none", "faas_cloudflare_origin_only": true,
			"faas_edge_cloudflare_ipv4_cidrs": []string{"173.245.48.0/20"}, "faas_edge_cloudflare_ipv6_cidrs": []string{"2400:cb00::/32"},
		})
		if line := "Environment=FAAS_EDGE_PROXY_CIDRS=173.245.48.0/20,2400:cb00::/32\n"; !strings.Contains(output, line) {
			t.Errorf("%q not rendered:\n%s", line, output)
		}
	})
	// ADR-20261103163954 §3: the database source reads no certificate or key
	// file; the seal_domains role hands the edge the edge-tls identity that
	// opens the stored key.
	t.Run("entry=both front=none certificate from the database", func(t *testing.T) {
		if defaults["faas_edge_public_tls_cert_source"] != "file" {
			t.Fatalf("faas_edge_public_tls_cert_source defaults to %v, want file", defaults["faas_edge_public_tls_cert_source"])
		}
		output := render(t, map[string]any{"faas_edge_entry": "both", "faas_edge_public_front": "none", "faas_edge_public_tls_cert_source": "database"})
		for _, line := range []string{
			"LoadCredential=edge_tls_client_ca:/etc/faas/secrets/edge-tls/origin-pull-ca.pem\n",
			"Environment=FAAS_EDGE_PUBLIC_TLS_CERT_SOURCE=database\n",
			"Environment=FAAS_EDGE_PUBLIC_TLS_CERT_CA=letsencrypt\n",
			noFront,
		} {
			if !strings.Contains(output, line) {
				t.Errorf("%q not rendered:\n%s", line, output)
			}
		}
		if strings.Contains(output, "edge_tls_cert") || strings.Contains(output, "edge_tls_key") || strings.Contains(output, "edge-tls.age") {
			t.Errorf("the database source rendered certificate files or the identity, which seal_domains owns:\n%s", output)
		}
	})
}

// The role removes the hand-made direct-entry drop-in before systemd reloads:
// its certificate files would stop an edge whose certificate comes from the
// database from starting.
func TestGatewaydPublicEdgeRemovesHandMadeDirectDropIn(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_public_edge_service", "tasks", "enable.yml"))
	if err != nil {
		t.Fatal(err)
	}
	enable := string(body)
	remove := strings.Index(enable, "60-faas-edge-direct.conf\n    state: absent")
	if remove < 0 {
		t.Fatal("the role must remove the hand-made 60-faas-edge-direct.conf drop-in")
	}
	if reload := strings.Index(enable, "reload systemd before enabling the edge"); reload < 0 || remove > reload {
		t.Fatal("the hand-made drop-in must go before systemd reloads the edge")
	}
}

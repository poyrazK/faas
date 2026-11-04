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

// edgednsd starts in dry run, reads its token only as a credential, and gets
// its names and node addresses from the role's drop-in.
func TestEdgednsdRoleDropIn(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "edgednsd_service")
	raw, err := os.ReadFile(filepath.Join(roleDir, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{}
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults["faas_edgednsd_dry_run"] != true {
		t.Fatalf("faas_edgednsd_dry_run defaults to %v, want true", defaults["faas_edgednsd_dry_run"])
	}
	if unit := UnitEdgednsd(); strings.Contains(unit.EnvironmentFile, "cf-token") {
		t.Fatal("the token must arrive through LoadCredential, not an environment file")
	}

	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
print(Environment(trim_blocks=True).from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	for _, tc := range []struct {
		dryRun any
		want   string
	}{{true, "1"}, {"True", "1"}, {false, "0"}} {
		values := map[string]any{}
		for k, v := range defaults {
			values[k] = v
		}
		values["faas_edgednsd_names"] = []string{"zone-1:*.gregale.dev", "zone-1:pilot.gregale.dev"}
		values["faas_edgednsd_node_addresses"] = []string{"fsn-2.faas=203.0.113.2"}
		values["faas_edgednsd_dry_run"] = tc.dryRun
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("python3", "-c", script, filepath.Join(roleDir, "templates", "50-faas-edgednsd.conf.j2"), string(encoded)).CombinedOutput()
		if err != nil {
			t.Fatalf("render: %v: %s", err, out)
		}
		for _, line := range []string{
			"LoadCredential=cf_token:/etc/faas/secrets/edgednsd/cf-token\n",
			"Environment=FAAS_EDGEDNS_CF_TOKEN_FILE=%d/cf_token\n",
			"Environment=FAAS_EDGEDNS_NAMES=zone-1:*.gregale.dev,zone-1:pilot.gregale.dev\n",
			"Environment=FAAS_EDGEDNS_NODE_ADDRESSES=fsn-2.faas=203.0.113.2\n",
			"Environment=FAAS_EDGEDNS_DRY_RUN=" + tc.want + "\n",
			"Environment=FAAS_EDGEDNS_METRICS_ADDR=127.0.0.1:9113\n",
		} {
			// Jinja drops the template's final newline.
			if !strings.Contains(string(out)+"\n", line) {
				t.Errorf("dry_run=%v: drop-in lacks %q:\n%s", tc.dryRun, line, out)
			}
		}
	}
}

// The certificate's settings render only with names, and its account key
// and the edge-tls recipient arrive as credentials (ADR-20261103163954 §3).
func TestEdgednsdRoleDropInCertificate(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "edgednsd_service")
	raw, err := os.ReadFile(filepath.Join(roleDir, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{}
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults["faas_edgednsd_acme_ca"] != "letsencrypt-staging" {
		t.Fatalf("faas_edgednsd_acme_ca defaults to %v, want letsencrypt-staging", defaults["faas_edgednsd_acme_ca"])
	}
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
print(Environment(trim_blocks=True).from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	render := func(names []string) string {
		values := map[string]any{}
		for k, v := range defaults {
			values[k] = v
		}
		values["faas_edgednsd_names"] = []string{"zone-1:*.gregale.dev:dns-only"}
		values["faas_edgednsd_node_addresses"] = []string{"fsn-2.faas=203.0.113.2"}
		values["faas_edgednsd_certificate_names"] = names
		values["faas_edgednsd_certificate_zone_id"] = "zone-1"
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("python3", "-c", script, filepath.Join(roleDir, "templates", "50-faas-edgednsd.conf.j2"), string(encoded)).CombinedOutput()
		if err != nil {
			t.Fatalf("render: %v: %s", err, out)
		}
		return string(out) + "\n"
	}
	if out := render(nil); strings.Contains(out, "FAAS_EDGEDNS_CERT") || strings.Contains(out, "acme") {
		t.Fatalf("no certificate names, yet the drop-in configures one:\n%s", out)
	}
	out := render([]string{"*.gregale.dev"})
	for _, line := range []string{
		"LoadCredential=acme_account_key:/etc/faas/secrets/edgednsd/acme-account.key\n",
		"LoadCredential=edge_tls_recipient:/etc/faas/secrets/domains/edge-tls.age.pub\n",
		"Environment=FAAS_EDGEDNS_CERT_NAMES=*.gregale.dev\n",
		"Environment=FAAS_EDGEDNS_CERT_ZONE_ID=zone-1\n",
		"Environment=FAAS_EDGEDNS_ACME_CA=letsencrypt-staging\n",
		"Environment=FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE=%d/acme_account_key\n",
		"Environment=FAAS_EDGEDNS_CERT_RECIPIENT_FILE=%d/edge_tls_recipient\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("drop-in lacks %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, "FAAS_EDGEDNS_ACME_EMAIL") {
		t.Errorf("an empty contact rendered:\n%s", out)
	}
}

// The Certificate Transparency watch renders only with a domain, and its
// Cert Spotter key arrives as a credential only when the operator staged one
// (ADR-20261103163954 §3).
func TestEdgednsdRoleDropInTransparency(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "edgednsd_service")
	raw, err := os.ReadFile(filepath.Join(roleDir, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{}
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	script := `import json, pathlib, sys
from jinja2 import Environment
print(Environment(trim_blocks=True).from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	render := func(overrides map[string]any) string {
		values := map[string]any{}
		for k, v := range defaults {
			values[k] = v
		}
		values["faas_edgednsd_names"] = []string{"zone-1:*.gregale.dev:dns-only"}
		values["faas_edgednsd_node_addresses"] = []string{"fsn-2.faas=203.0.113.2"}
		for k, v := range overrides {
			values[k] = v
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("python3", "-c", script, filepath.Join(roleDir, "templates", "50-faas-edgednsd.conf.j2"), string(encoded)).CombinedOutput()
		if err != nil {
			t.Fatalf("render: %v: %s", err, out)
		}
		return string(out) + "\n"
	}
	if out := render(nil); strings.Contains(out, "FAAS_EDGEDNS_CT_") || strings.Contains(out, "certspotter") {
		t.Fatalf("no watch domain, yet the drop-in configures the watch:\n%s", out)
	}
	out := render(map[string]any{"faas_edgednsd_ct_domain": "gregale.dev", "faas_edgednsd_ct_allow_names": []string{"*.gregale.dev", "gregale.dev"}})
	for _, line := range []string{
		"Environment=FAAS_EDGEDNS_CT_DOMAIN=gregale.dev\n",
		"Environment=FAAS_EDGEDNS_CT_ALLOW_NAMES=*.gregale.dev,gregale.dev\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("drop-in lacks %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, "certspotter") || strings.Contains(out, "FAAS_EDGEDNS_CT_API_KEY_FILE") {
		t.Errorf("a key the operator did not stage rendered:\n%s", out)
	}
	out = render(map[string]any{"faas_edgednsd_ct_domain": "gregale.dev", "faas_edgednsd_ct_api_key_staged": true})
	for _, line := range []string{
		"LoadCredential=certspotter_api_key:/etc/faas/secrets/edgednsd/certspotter-api-key\n",
		"Environment=FAAS_EDGEDNS_CT_API_KEY_FILE=%d/certspotter_api_key\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("drop-in lacks %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, "FAAS_EDGEDNS_CT_ALLOW_NAMES") {
		t.Errorf("an empty allow list rendered:\n%s", out)
	}
}

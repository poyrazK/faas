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

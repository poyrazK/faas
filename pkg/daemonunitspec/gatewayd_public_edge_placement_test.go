// adr: 20261031105055
package daemonunitspec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Placement routing is off unless the manifest turns it on, reaches the edge
// only through the Ansible-rendered drop-in, and the role removes the
// hand-made drop-in it replaces.
func TestGatewaydPublicEdgePlacementRoutingSwitch(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_public_edge_service")
	read := func(rel string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(roleDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	var defaults struct {
		Placement bool `yaml:"faas_edge_placement_routing"`
	}
	if err := yaml.Unmarshal([]byte(read("defaults/main.yml")), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Placement {
		t.Error("placement routing must default off; the manifest turns it on per cell")
	}
	if enable := read("tasks/enable.yml"); !strings.Contains(enable, "60-faas-edge-placement.conf\n    state: absent") {
		t.Error("the role must remove the hand-made 60-faas-edge-placement.conf drop-in")
	}

	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	template := filepath.Join(roleDir, "templates", "99-faas-edge.conf.j2")
	script := `import pathlib, sys
from jinja2 import Environment
env = Environment(trim_blocks=True)
print(env.from_string(pathlib.Path(sys.argv[1]).read_text()).render(
    faas_edge_control_plane_target="http://10.42.0.1:8090", faas_edge_placement_routing=sys.argv[2]), end="")
`
	for _, tc := range []struct {
		value string
		want  bool
	}{{"true", true}, {"True", true}, {"false", false}} {
		t.Run(tc.value, func(t *testing.T) {
			output, err := exec.Command("python3", "-c", script, template, tc.value).CombinedOutput()
			if err != nil {
				t.Fatalf("standard Jinja render failed: %v: %s", err, output)
			}
			if got := strings.Contains(string(output), "Environment=FAAS_EDGE_PLACEMENT_ROUTING=1\n"); got != tc.want {
				t.Errorf("FAAS_EDGE_PLACEMENT_ROUTING rendered = %v, want %v:\n%s", got, tc.want, output)
			}
		})
	}
}

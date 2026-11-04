// adr: 20261102081618, 20261103163954
package daemonunitspec

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The §5 switches, and the edge's client limits (ADR-20261103163954 §4), reach
// the edge and gatewayd-internal only through their roles' drop-ins, and only
// when set.
func TestEdgeSpreadSwitchesRender(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	// Ansible's bool filter, for the templates that use it.
	script := `import json, pathlib, sys
from jinja2 import Environment
env = Environment(trim_blocks=True)
env.filters["bool"] = lambda v: str(v).lower() in ("true", "yes", "on", "1")
print(env.from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	for _, tc := range []struct {
		template, variable, line string
		base                     map[string]any
	}{
		{
			template: filepath.Join(roles, "gatewayd_public_edge_service", "templates", "99-faas-edge.conf.j2"),
			variable: "faas_edge_placement_node_set",
			line:     "Environment=FAAS_EDGE_PLACEMENT_NODE_SET=1\n",
			base:     map[string]any{"faas_edge_control_plane_target": "http://10.42.0.1:8090"},
		},
		{
			template: filepath.Join(roles, "gatewayd_public_edge_service", "templates", "99-faas-edge.conf.j2"),
			variable: "faas_edge_client_limits",
			line:     "Environment=FAAS_EDGE_CLIENT_LIMITS=1\n",
			base:     map[string]any{"faas_edge_control_plane_target": "http://10.42.0.1:8090"},
		},
		{
			template: filepath.Join(roles, "gatewayd_internal_service", "templates", "99-faas-role.conf.j2"),
			variable: "faas_gateway_edge_local_first",
			line:     "Environment=FAAS_GATEWAY_EDGE_LOCAL_FIRST=1\n",
			base:     map[string]any{"faas_box_role": "compute-only"},
		},
	} {
		for _, value := range []any{true, "True", false, nil} {
			values := map[string]any{}
			for k, v := range tc.base {
				values[k] = v
			}
			if value != nil {
				values[tc.variable] = value
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("python3", "-c", script, tc.template, string(encoded)).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: render: %v: %s", tc.template, err, out)
			}
			want := value == true || value == "True"
			if got := strings.Contains(string(out)+"\n", tc.line); got != want {
				t.Errorf("%s with %s=%v rendered the switch = %v, want %v:\n%s", filepath.Base(tc.template), tc.variable, value, got, want, out)
			}
		}
	}
}

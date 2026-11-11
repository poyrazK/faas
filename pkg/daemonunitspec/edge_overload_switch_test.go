// adr: 20261110123517
package daemonunitspec

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FAAS_EDGE_OVERLOAD reaches the compute edge only with the client limits on,
// and only as observe or on.
func TestEdgeOverloadSwitchRenders(t *testing.T) {
	if err := exec.Command("python3", "-c", "import jinja2").Run(); err != nil {
		t.Skip("python3 with jinja2 required for drop-in rendering")
	}
	template := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "gatewayd_public_edge_service", "templates", "99-faas-edge.conf.j2")
	script := `import json, pathlib, sys
from jinja2 import Environment
env = Environment(trim_blocks=True)
print(env.from_string(pathlib.Path(sys.argv[1]).read_text()).render(**json.loads(sys.argv[2])), end="")
`
	for _, tc := range []struct {
		limits   any
		overload any
		want     string
	}{
		{limits: true, overload: "observe", want: "Environment=FAAS_EDGE_OVERLOAD=observe\n"},
		{limits: true, overload: "On", want: "Environment=FAAS_EDGE_OVERLOAD=on\n"},
		{limits: true, overload: "off"},
		{limits: true, overload: nil},
		{limits: true, overload: true},
		{limits: false, overload: "on"},
	} {
		values := map[string]any{"faas_edge_control_plane_target": "http://10.42.0.1:8090", "faas_edge_client_limits": tc.limits}
		if tc.overload != nil {
			values["faas_edge_overload"] = tc.overload
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("python3", "-c", script, template, string(encoded)).CombinedOutput()
		if err != nil {
			t.Fatalf("render: %v: %s", err, out)
		}
		got := ""
		for _, line := range strings.SplitAfter(string(out)+"\n", "\n") {
			if strings.HasPrefix(line, "Environment=FAAS_EDGE_OVERLOAD=") {
				got = line
			}
		}
		if got != tc.want {
			t.Errorf("limits=%v overload=%v rendered %q, want %q", tc.limits, tc.overload, got, tc.want)
		}
	}
}

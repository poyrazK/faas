package main

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// The control plane's expected-probe inventory and each compute node's probe
// must read the same fleet, keyed by the name the probe looks itself up by
// (faas_node_name). Without a serving_probes section nothing is rendered and
// the roles keep their unconfigured default.
func TestRenderManifestAnsibleFiles_ServingProbes(t *testing.T) {
	base := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n", 1)
	render := func(t *testing.T, doc string) map[string]map[string]any {
		t.Helper()
		m, err := manifest.Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if errs := m.Validate(); errs != nil {
			t.Fatal(errs)
		}
		files, err := renderManifestAnsibleFiles(m, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		hosts := map[string]map[string]any{}
		for _, f := range files {
			for _, name := range []string{"fsn-1", "fsn-2"} {
				if strings.HasSuffix(f.Path, "/host_vars/"+name+".yml") {
					vars := map[string]any{}
					if err := yaml.Unmarshal(f.Body, &vars); err != nil {
						t.Fatalf("%s host vars are not YAML: %v\n%s", name, err, f.Body)
					}
					hosts[name] = vars
				}
			}
		}
		if len(hosts) != 2 {
			t.Fatalf("rendered %d host files, want 2", len(hosts))
		}
		return hosts
	}
	for name, vars := range render(t, base) {
		if _, ok := vars["faas_serving_probe_fleet"]; ok {
			t.Fatalf("%s: manifest without serving_probes rendered a fleet", name)
		}
	}

	configured := base + "serving_probes:\n  urls: [\"https://serving-probe.gregale.dev/\"]\n  expected_body: 'probe \"ok\" sağlıklı'\n  interval: 5min\n"
	want := map[string]any{"fsn-2.faas": map[string]any{
		"urls":          []any{"https://serving-probe.gregale.dev/"},
		"expected_body": `probe "ok" sağlıklı`,
		"interval":      "5min",
	}}
	for name, vars := range render(t, configured) {
		if got := vars["faas_serving_probe_fleet"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: faas_serving_probe_fleet = %#v, want %#v", name, got, want)
		}
	}

	m, err := manifest.Parse([]byte(base + "serving_probes:\n  urls: [\"http://serving-probe.gregale.dev/\"]\n  expected_body: ok\n"))
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.Validate(); errs == nil {
		t.Fatal("a plain-http probe URL validated")
	}
}

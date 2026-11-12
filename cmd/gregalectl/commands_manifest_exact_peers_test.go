package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-20261110120000: every control-plane and compute host names the exact
// peers its firewall admits: IP-literal hosts as /32s, hostname hosts by
// inventory name. No provider IP is written for a hostname host, and the
// *_allowed_cidrs lists other roles read are unchanged.
func TestRenderManifestAnsibleFiles_ExactPeers(t *testing.T) {
	render := func(t *testing.T, hosts string) map[string]string {
		t.Helper()
		yaml := strings.Replace(validManifestYAML, "    - name: fsn-1\n      role: control-plane\n", hosts, 1)
		m, err := manifest.Parse([]byte(yaml))
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
		out := map[string]string{}
		for _, f := range files {
			for _, name := range []string{"fsn-1", "fsn-2", "fsn-3"} {
				if strings.HasSuffix(f.Path, "/"+name+".yml") {
					out[name] = string(f.Body)
				}
			}
		}
		return out
	}
	named := render(t, "    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:7100\n"+
		"    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n"+
		"    - name: fsn-3\n      role: compute-only\n      address: fsn-3.gregale.dev:50051\n")
	wants := map[string][]string{
		"fsn-1": {
			`faas_compute_allowed_exact: {cidrs: [], hosts: ["fsn-2", "fsn-3"]}`,
			`faas_compute_allowed_cidrs: ["10.42.0.0/24"]`,
		},
		"fsn-2": {
			`faas_control_plane_allowed_exact: {cidrs: [], hosts: ["fsn-1"]}`,
			`faas_compute_peer_exact: {cidrs: [], hosts: ["fsn-3"]}`,
			`faas_control_plane_allowed_cidrs: ["10.42.0.0/24"]`,
		},
	}
	for host, lines := range wants {
		for _, want := range lines {
			if !strings.Contains(named[host], want) {
				t.Errorf("%s vars lack %q:\n%s", host, want, named[host])
			}
		}
	}
	ipv4 := regexp.MustCompile(`_exact: .*\d+\.\d+\.\d+\.\d+`)
	for host, vars := range named {
		if ipv4.MatchString(vars) {
			t.Errorf("%s: hostname fleet wrote an address into an exact list:\n%s", host, vars)
		}
	}

	literal := render(t, "    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n"+
		"    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n"+
		"    - name: fsn-3\n      role: compute-only\n      address: 10.42.0.3:50051\n")
	for host, want := range map[string]string{
		"fsn-1": `faas_compute_allowed_exact: {cidrs: ["10.42.0.2/32", "10.42.0.3/32"], hosts: []}`,
		"fsn-2": `faas_compute_peer_exact: {cidrs: ["10.42.0.3/32"], hosts: []}`,
		"fsn-3": `faas_compute_peer_exact: {cidrs: ["10.42.0.2/32"], hosts: []}`,
	} {
		if !strings.Contains(literal[host], want) {
			t.Errorf("%s vars lack %q:\n%s", host, want, literal[host])
		}
	}
}

package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-20261110120000: with hostnames as addresses the firewall used to admit
// the whole overlay subnet; private_ipv4 narrows every allow list to the
// exact hosts.
func TestRenderManifestAnsibleFiles_PrivateAddressFirewall(t *testing.T) {
	render := func(t *testing.T, hosts string) (cp, compute string) {
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
		for _, f := range files {
			switch {
			case strings.HasSuffix(f.Path, "fsn-1.yml"):
				cp = string(f.Body)
			case strings.HasSuffix(f.Path, "fsn-2.yml"):
				compute = string(f.Body)
			}
		}
		return cp, compute
	}
	named := "    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:7100\n" +
		"    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n"
	cp, compute := render(t, named)
	if !strings.Contains(compute, `faas_control_plane_allowed_cidrs: ["10.42.0.0/24"]`) || !strings.Contains(cp, `faas_compute_allowed_cidrs: ["10.42.0.0/24"]`) {
		t.Fatalf("hostnames without private_ipv4 keep the subnet:\n%s\n%s", cp, compute)
	}
	private := "    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:7100\n      private_ipv4: 10.42.0.1\n" +
		"    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n      private_ipv4: 10.42.0.2\n"
	cp, compute = render(t, private)
	for _, want := range []string{`faas_compute_allowed_cidrs: ["10.42.0.2/32"]`, `faas_postgres_allowed_cidrs: ["10.42.0.2/32"]`} {
		if !strings.Contains(cp, want) {
			t.Errorf("control plane vars missing %q:\n%s", want, cp)
		}
	}
	if !strings.Contains(compute, `faas_control_plane_allowed_cidrs: ["10.42.0.1/32"]`) {
		t.Errorf("compute vars do not admit only the control plane:\n%s", compute)
	}
}

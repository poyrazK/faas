package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-20261109170000: the manifest's artifact_signer runs signd on the
// control plane's private address and points every compute node's imaged at
// it; retire_compute_key then takes sign.key off the compute nodes.
func TestRenderManifestAnsibleFiles_ArtifactSigner(t *testing.T) {
	base := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n", 1)
	render := func(t *testing.T, yaml string) (cp, compute string) {
		t.Helper()
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

	cp, compute := render(t, base)
	if strings.Contains(cp+compute, "signd") || strings.Contains(compute, "sign_key_retired") {
		t.Fatalf("manifest without artifact_signer rendered signer vars:\n%s\n%s", cp, compute)
	}

	cp, compute = render(t, base+"artifact_signer:\n  enabled: true\n")
	if !strings.Contains(cp, `faas_signd_listen: "tcp://10.42.0.1:8097"`) {
		t.Errorf("control plane vars:\n%s", cp)
	}
	for _, want := range []string{`faas_imaged_signd_target: "tcp://10.42.0.1:8097"`, "faas_compute_sign_key_retired: false"} {
		if !strings.Contains(compute, want) {
			t.Errorf("compute vars missing %q:\n%s", want, compute)
		}
	}
	if strings.Contains(cp, "faas_imaged_signd_target") || strings.Contains(compute, "faas_signd_listen") {
		t.Errorf("signer vars on the wrong role:\n%s\n%s", cp, compute)
	}

	_, compute = render(t, base+"artifact_signer:\n  enabled: true\n  port: 9443\n  retire_compute_key: true\n")
	for _, want := range []string{`faas_imaged_signd_target: "tcp://10.42.0.1:9443"`, "faas_compute_sign_key_retired: true"} {
		if !strings.Contains(compute, want) {
			t.Errorf("compute vars missing %q:\n%s", want, compute)
		}
	}
}

func TestArtifactSignerManifestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    manifest.ArtifactSigner
		ok   bool
	}{
		{"absent", manifest.ArtifactSigner{}, true},
		{"enabled", manifest.ArtifactSigner{Enabled: true}, true},
		{"retire without signd", manifest.ArtifactSigner{RetireComputeKey: true}, false},
		{"port without signd", manifest.ArtifactSigner{Port: 9443}, false},
		{"privileged port", manifest.ArtifactSigner{Enabled: true, Port: 443}, false},
	} {
		yaml := strings.Replace(validManifestYAML,
			"    - name: fsn-1\n      role: control-plane\n",
			"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n", 1)
		var b strings.Builder
		if tc.a != (manifest.ArtifactSigner{}) {
			b.WriteString("artifact_signer:\n")
			if tc.a.Enabled {
				b.WriteString("  enabled: true\n")
			}
			if tc.a.Port != 0 {
				b.WriteString("  port: " + strconv.Itoa(tc.a.Port) + "\n")
			}
			if tc.a.RetireComputeKey {
				b.WriteString("  retire_compute_key: true\n")
			}
		}
		m, err := manifest.Parse([]byte(yaml + b.String()))
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		errs := m.Validate()
		hasSignerErr := strings.Contains(fmt.Sprint(errs), "artifact_signer")
		if hasSignerErr == tc.ok {
			t.Errorf("%s: validation errors = %v, want ok=%v", tc.name, errs, tc.ok)
		}
	}
}

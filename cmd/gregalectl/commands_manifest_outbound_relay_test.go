package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-989: the manifest always points compute vmmd at outboundd.faas:8096,
// but the relay only opens when outbound_relay turns on outboundd's listener
// on the control plane and each compute node's relay client leaf. Before
// this switch a live fleet rendered the target alone, so vmmd logged
// "outbound relay disabled" and apps' GREGALE_OUTBOUND_URL was dead.
func TestRenderManifestAnsibleFiles_OutboundRelay(t *testing.T) {
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
	if strings.Contains(cp+compute, "faas_outboundd_relay_listen") || strings.Contains(cp+compute, "faas_vmmd_outbound_relay_tls_managed") {
		t.Fatalf("manifest without outbound_relay opened the relay:\n%s\n%s", cp, compute)
	}
	if !strings.Contains(compute, `faas_vmmd_outbound_relay_target: "tcp://outboundd.faas:8096"`) {
		t.Fatalf("compute vars lost the relay target:\n%s", compute)
	}

	cp, compute = render(t, base+"outbound_relay:\n  enabled: true\n")
	if !strings.Contains(cp, `faas_outboundd_relay_listen: "tcp://0.0.0.0:8096"`) {
		t.Errorf("control plane vars missing the relay listener:\n%s", cp)
	}
	if !strings.Contains(compute, "faas_vmmd_outbound_relay_tls_managed: true") {
		t.Errorf("compute vars missing the relay client leaf switch:\n%s", compute)
	}
	if strings.Contains(cp, "faas_vmmd_outbound_relay_tls_managed") || strings.Contains(compute, "faas_outboundd_relay_listen") {
		t.Errorf("relay vars on the wrong role:\n%s\n%s", cp, compute)
	}
}

// The live OVH manifest that CD renders opens the relay: the cluster
// workload-identity key is installed there, and stateless agents and the AI
// gateway need GREGALE_OUTBOUND_URL.
func TestOVHLiveManifestOpensOutboundRelay(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "manifest", "production", "ovh-live.template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\noutbound_relay:\n  enabled: true\n") {
		t.Fatal("ovh-live.template.yaml does not set outbound_relay.enabled: true")
	}
}

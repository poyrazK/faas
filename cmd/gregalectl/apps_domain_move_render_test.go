// adr: 20261112114642
package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// The gregale.app move is a manifest change: apps under gregale.app, the
// site, dashboard and API under gregale.dev, gregale.dev app hosts kept as an
// alias, and direct entry (records, certificate, Certificate Transparency
// watch) on gregale.app only. This renders that target manifest and checks no
// direct-entry variable names the platform domain; adding a platform name
// to the certificate is refused before anything renders.
func TestRenderManifestAnsibleFiles_AppsDomainMoveTarget(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n      public_ipv4: 203.0.113.2\n      entry_ipv4: 198.51.100.2\n", 1)
	yaml = strings.Replace(yaml, "dns:\n  apps_domain: apps.gregale.dev\n  mode: cloudflare\n",
		"dns:\n  apps_domain: gregale.app\n  mode: cloudflare\n  platform_domain: gregale.dev\n  deploy_domain: gregale.app\n  apps_domain_aliases: [gregale.dev]\n", 1)
	edge := "public_edge:\n  compute_nodes: true\n  entry: both\n  front: none\n  dns:\n    zone_id: zone-app\n    names: [\"*.gregale.app\"]\n" +
		"    certificate:\n      names: [\"gregale.app\", \"*.gregale.app\"]\n      ca: letsencrypt\n      serve: letsencrypt\n" +
		"      transparency:\n        domain: gregale.app\n"

	m, err := manifest.Parse([]byte(yaml + edge))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}
	files, err := renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles: %v", err)
	}
	var controlVars, computeVars string
	for _, file := range files {
		switch {
		case strings.HasSuffix(file.Path, "fsn-1.yml"):
			controlVars = string(file.Body)
		case strings.HasSuffix(file.Path, "fsn-2.yml"):
			computeVars = string(file.Body)
		}
	}
	for _, want := range []string{
		`gatewayd_apps_domain: "gregale.app"`,
		`faas_platform_domain: "gregale.dev"`,
		`faas_deploy_domain: "gregale.app"`,
		`faas_apps_domain_aliases: ["gregale.dev"]`,
	} {
		for host, vars := range map[string]string{"control plane": controlVars, "compute": computeVars} {
			if !strings.Contains(vars, want) {
				t.Errorf("%s vars missing %s:\n%s", host, want, vars)
			}
		}
	}
	for _, want := range []string{
		"faas_edgednsd_names: [\"zone-app:*.gregale.app:dns-only\"]\n",
		"faas_edgednsd_certificate_names: [\"gregale.app\", \"*.gregale.app\"]\n",
		"faas_edgednsd_certificate_zone_id: \"zone-app\"\n",
		"faas_edgednsd_ct_domain: \"gregale.app\"\n",
	} {
		if !strings.Contains(controlVars, want) {
			t.Errorf("control plane vars missing %q:\n%s", want, controlVars)
		}
	}
	if !strings.Contains(computeVars, "faas_edge_public_tls_cert_source: \"database\"\n") {
		t.Errorf("compute edges do not serve the stored certificate:\n%s", computeVars)
	}
	for _, line := range strings.Split(controlVars+computeVars, "\n") {
		if strings.HasPrefix(line, "faas_edgednsd_") && strings.Contains(line, "gregale.dev") {
			t.Errorf("direct entry names the platform domain: %s", line)
		}
	}

	// The R8-6 guard: the platform domain on the certificate fails validation.
	leaky := strings.Replace(edge, `names: ["gregale.app", "*.gregale.app"]`, `names: ["*.gregale.app", "*.gregale.dev"]`, 1)
	m, err = manifest.Parse([]byte(yaml + leaky))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	errs := m.Validate()
	if !strings.Contains(errs.Error(), "public_edge.dns.certificate.names[1]") {
		t.Fatalf("a certificate naming *.gregale.dev passed validation: %v", errs)
	}
}

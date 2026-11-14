// adr: 576 — the manifest's service_tcp block stages private TCP service
// addressing on compute-only hosts in the ADR's rollout order; a fleet
// without it renders as before.
package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// The shared fleet declares database hosts, which need managed_postgres.
const serviceTCPManagedPostgres = "managed_postgres:\n  host_suffix: db.gregale.dev\n"

func TestRenderManifestAnsibleFiles_ServiceTCPStages(t *testing.T) {
	const (
		node     = "faas_service_tcp_enabled: true\n"
		https    = `faas_service_tcp_https_enabled: "{{ gatewayd_service_proxy_https_listen | default('') | length > 0 }}"`
		listener = `gatewayd_service_tcp_listen: "{{ gatewayd_service_proxy_listen | regex_replace(':[0-9]+$', ':10082') }}"`
		dns      = "gatewayd_service_tcp_dns: true\n"
	)
	for _, tc := range []struct {
		name  string
		block string
		want  []string
	}{
		{"absent", "", nil},
		{"all off", "service_tcp: {}\n", nil},
		{"node", "service_tcp:\n  node: true\n", []string{node, https}},
		{"listener", "service_tcp:\n  node: true\n  listener: true\n", []string{node, https, listener}},
		{"dns", "service_tcp:\n  node: true\n  listener: true\n  dns: true\n", []string{node, https, listener, dns}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := renderedFiles(t, databaseManifest(t, serviceTCPManagedPostgres+tc.block))
			compute := files["fsn-2.yml"]
			for _, want := range []string{node, https, listener, dns} {
				wanted := false
				for _, w := range tc.want {
					wanted = wanted || w == want
				}
				if strings.Contains(compute, want) != wanted {
					t.Errorf("compute vars contain %q = %v, want %v:\n%s", want, !wanted, wanted, compute)
				}
			}
			for _, other := range []string{"fsn-1.yml", "db-1.yml"} {
				if strings.Contains(files[other], "service_tcp") {
					t.Errorf("%s carries service TCP vars:\n%s", other, files[other])
				}
			}
		})
	}
}

func TestManifestServiceTCPStagesNeedTheOneBefore(t *testing.T) {
	for _, tc := range []struct {
		block, path string
	}{
		{"service_tcp:\n  listener: true\n", "service_tcp.listener"},
		{"service_tcp:\n  node: true\n  dns: true\n", "service_tcp.dns"},
	} {
		yaml := strings.Replace(validManifestYAML, "    - name: fsn-1\n      role: control-plane\n", databaseFleetHosts, 1) + serviceTCPManagedPostgres + tc.block
		m, err := manifest.Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("manifest.Parse: %v", err)
		}
		errs := m.Validate()
		found := false
		for _, e := range errs {
			found = found || e.Path == tc.path
		}
		if !found {
			t.Errorf("%q validated with %v, want an error at %s", tc.block, errs, tc.path)
		}
	}
}

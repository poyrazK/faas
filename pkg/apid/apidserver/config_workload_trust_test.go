package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// adr: 1141
func TestWorkloadTrustPrecedence(t *testing.T) {
	cluster := apidserver.Config{WorkloadJWKSPath: "/etc/faas/secrets/workload-identity.jwks.json", WorkloadIssuer: "https://identity.example.test"}
	for _, tc := range []struct {
		name       string
		cfg        apidserver.Config
		env        map[string]string
		path       string
		wantPath   string
		wantIssuer string
	}{
		{name: "nothing configured", wantIssuer: workloadidentity.DefaultIssuer},
		{name: "cluster trust is the fallback", cfg: cluster, wantPath: cluster.WorkloadJWKSPath, wantIssuer: cluster.WorkloadIssuer},
		{name: "cluster env beats cluster TOML", cfg: cluster, env: map[string]string{"FAAS_WORKLOAD_JWKS_PATH": "/env/cluster.json"},
			wantPath: "/env/cluster.json", wantIssuer: cluster.WorkloadIssuer},
		{name: "surface TOML beats cluster trust", cfg: cluster, path: "/flags.json", wantPath: "/flags.json", wantIssuer: cluster.WorkloadIssuer},
		{name: "surface env beats everything", cfg: cluster, path: "/flags.json",
			env:      map[string]string{"FAAS_FLAGS_WORKLOAD_JWKS_PATH": "/env/flags.json", "FAAS_WORKLOAD_JWKS_PATH": "/env/cluster.json"},
			wantPath: "/env/flags.json", wantIssuer: cluster.WorkloadIssuer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			path, issuer := apidserver.WorkloadTrust(tc.cfg, getenv, "FAAS_FLAGS_WORKLOAD_JWKS_PATH", tc.path, "FAAS_FLAGS_WORKLOAD_ISSUER", "")
			if path != tc.wantPath || issuer != tc.wantIssuer {
				t.Fatalf("got (%q, %q), want (%q, %q)", path, issuer, tc.wantPath, tc.wantIssuer)
			}
		})
	}
}

package main

import (
	"strings"
	"testing"
)

// ADR-20261110210000 slice 3g-3: a retired imaged pulls through secretd and
// holds no build, fleet or host identity.
func TestBuildRetiredFromEnv(t *testing.T) {
	const on = "FAAS_SEAL_DOMAIN_BUILD_RETIRED"
	for _, tc := range []struct {
		name      string
		env       map[string]string
		viaSecret bool
		want      bool
		wantErr   string
	}{
		{name: "unset", env: map[string]string{}},
		{name: "false", env: map[string]string{on: "false", "FAAS_HOST_AGE_IDENTITY_PATH": "/run/credentials/x/host"}},
		{name: "retired", env: map[string]string{on: "true"}, viaSecret: true, want: true},
		{name: "bad value", env: map[string]string{on: "1"}, viaSecret: true, wantErr: "want true or false"},
		{name: "local pulls", env: map[string]string{on: "true"}, wantErr: "FAAS_IMAGED_REGISTRY_PULLS=secretd"},
		{name: "build identity left", env: map[string]string{on: "true", "FAAS_SEAL_DOMAIN_BUILD_IDENTITY_PATH": "/run/credentials/x/faas_seal_domain_build"}, viaSecret: true, wantErr: "remove the build identity"},
		{name: "not contracted", env: map[string]string{on: "true", "FAAS_HOST_AGE_IDENTITY_PATH": "/run/credentials/x/host"}, viaSecret: true, wantErr: "build contract step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildRetiredFromEnv(func(k string) string { return tc.env[k] }, tc.viaSecret)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("retired = %v, want %v", got, tc.want)
			}
		})
	}
}

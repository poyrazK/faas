package main

import (
	"strings"
	"testing"
)

func TestEdgeRetiredFromEnv(t *testing.T) {
	retired := map[string]string{
		"FAAS_SEAL_DOMAIN_EDGE_RETIRED":    "true",
		"FAAS_GATEWAY_BASIC_AUTH":          "verifier-only",
		"FAAS_GATEWAY_LOG_DRAIN_OPENER":    "secretd",
		"FAAS_GATEWAY_EDGE_HEADER_SECRETS": "inject",
	}
	with := func(k, v string) map[string]string {
		out := map[string]string{}
		for key, val := range retired {
			out[key] = val
		}
		out[k] = v
		return out
	}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		want    bool
		wantErr string
	}{
		{name: "unset", env: map[string]string{}},
		{name: "false", env: map[string]string{"FAAS_SEAL_DOMAIN_EDGE_RETIRED": "false", "FAAS_SEAL_DOMAIN_EDGE_IDENTITY_PATH": "/run/x"}},
		{name: "retired", env: retired, want: true},
		{name: "bad value", env: with("FAAS_SEAL_DOMAIN_EDGE_RETIRED", "yes"), wantErr: "want true or false"},
		{name: "sealed basic auth", env: with("FAAS_GATEWAY_BASIC_AUTH", "verifier"), wantErr: "FAAS_GATEWAY_BASIC_AUTH=verifier-only"},
		{name: "local log drains", env: with("FAAS_GATEWAY_LOG_DRAIN_OPENER", ""), wantErr: "FAAS_GATEWAY_LOG_DRAIN_OPENER=secretd"},
		{name: "local edge headers", env: with("FAAS_GATEWAY_EDGE_HEADER_SECRETS", "local"), wantErr: "FAAS_GATEWAY_EDGE_HEADER_SECRETS=inject"},
		{name: "edge identity left", env: with("FAAS_SEAL_DOMAIN_EDGE_IDENTITY_PATH", "/run/credentials/x/faas_seal_domain_edge"), wantErr: "remove the edge identity"},
		{name: "fleet identity left", env: with("FAAS_HOST_KEY_PATH", "/run/credentials/x/host.age"), wantErr: "edge contract step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := edgeRetiredFromEnv(func(k string) string { return tc.env[k] })
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

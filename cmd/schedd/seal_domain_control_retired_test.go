package main

// adr: 20261110210000

import (
	"path/filepath"
	"strings"
	"testing"
)

// Slice 3k-2: a retired schedd mints through secretd and holds neither a
// control nor a fleet identity.
func TestControlRetiredFromEnv(t *testing.T) {
	contracted := t.TempDir()
	writeTestIdentity(t, filepath.Join(contracted, "host.age"))
	withFleet := t.TempDir()
	writeTestIdentity(t, filepath.Join(withFleet, "fleet.age"))
	writeTestIdentity(t, filepath.Join(withFleet, "host.age"))

	for _, tc := range []struct {
		name    string
		env     map[string]string
		remote  bool
		dir     string
		want    bool
		wantErr string
	}{
		{name: "unset", env: map[string]string{}, dir: withFleet},
		{name: "false", env: map[string]string{"FAAS_SEAL_DOMAIN_CONTROL_RETIRED": "false"}, dir: withFleet},
		{name: "retired", env: map[string]string{"FAAS_SEAL_DOMAIN_CONTROL_RETIRED": "true"}, remote: true, dir: contracted, want: true},
		{name: "bad value", env: map[string]string{"FAAS_SEAL_DOMAIN_CONTROL_RETIRED": "1"}, remote: true, dir: contracted, wantErr: "want true or false"},
		{name: "local minter", env: map[string]string{"FAAS_SEAL_DOMAIN_CONTROL_RETIRED": "true"}, dir: contracted, wantErr: "FAAS_SCHEDD_INTERNAL_SVC_MINTER=secretd"},
		{name: "control identity left", env: map[string]string{"FAAS_SEAL_DOMAIN_CONTROL_RETIRED": "true", "FAAS_SEAL_DOMAIN_CONTROL_IDENTITY_PATH": "/run/credentials/x"}, remote: true, dir: contracted, wantErr: "remove the control identity"},
		{name: "fleet identity left", env: map[string]string{"FAAS_SEAL_DOMAIN_CONTROL_RETIRED": "true"}, remote: true, dir: withFleet, wantErr: "cluster signing key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := controlRetiredFromEnv(func(k string) string { return tc.env[k] }, tc.remote, tc.dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("retired = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// A retired domain exports no faas_seal_domain_loaded series, so the
// missing-identity alert does not fire on a node meant not to hold it.
func TestLoadedSealDomainsSkipsRetired(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := loadedSealDomains(get, true, false); len(got) != 2 || !got["control"] || got["delivery"] {
		t.Fatalf("not retired = %v", got)
	}
	env["FAAS_SEAL_DOMAIN_CONTROL_RETIRED"] = "true"
	if got := loadedSealDomains(get, false, true); len(got) != 1 || !got["delivery"] {
		t.Fatalf("control retired = %v", got)
	}
	env["FAAS_SEAL_DOMAIN_DELIVERY_RETIRED"] = "true"
	if got := loadedSealDomains(get, false, false); len(got) != 0 {
		t.Fatalf("both retired = %v", got)
	}
}

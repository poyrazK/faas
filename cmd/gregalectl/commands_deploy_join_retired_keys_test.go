package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetiredComputeKeys(t *testing.T) {
	cases := []struct {
		name         string
		vars         string
		manifestSign bool
		wantSign     bool
		wantFleet    bool
	}{
		{name: "no switches", vars: "faas_other: true\n"},
		{name: "both retired", vars: "faas_compute_sign_key_retired: true\nfaas_compute_fleet_key_retired: true\n", wantSign: true, wantFleet: true},
		{name: "sign only, ansible string bool", vars: "faas_compute_sign_key_retired: \"yes\"\n", wantSign: true},
		{name: "fleet only, int", vars: "faas_compute_fleet_key_retired: 1\n", wantFleet: true},
		{name: "explicit false", vars: "faas_compute_sign_key_retired: false\nfaas_compute_fleet_key_retired: \"no\"\n"},
		{name: "empty file", vars: ""},
		{name: "manifest retires sign", vars: "faas_other: 1\n", manifestSign: true, wantSign: true},
		{name: "vars file overrides manifest", vars: "faas_compute_sign_key_retired: false\n", manifestSign: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ansible-vars.yml")
			if err := os.WriteFile(path, []byte(tc.vars), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := retiredComputeKeys(path, tc.manifestSign)
			if err != nil {
				t.Fatalf("retiredComputeKeys: %v", err)
			}
			if got.signKey != tc.wantSign || got.fleetKey != tc.wantFleet {
				t.Fatalf("got sign=%t fleet=%t, want sign=%t fleet=%t", got.signKey, got.fleetKey, tc.wantSign, tc.wantFleet)
			}
		})
	}

	if got, err := retiredComputeKeys("", false); err != nil || got != (computeKeyRetirement{}) {
		t.Fatalf("no vars file: got %+v, %v; want nothing retired", got, err)
	}
	if got, err := retiredComputeKeys("", true); err != nil || !got.signKey || got.fleetKey {
		t.Fatalf("no vars file, manifest retires sign: got %+v, %v", got, err)
	}
	if _, err := retiredComputeKeys(filepath.Join(t.TempDir(), "missing.yml"), false); err == nil {
		t.Fatal("a missing vars file must be an error")
	}
	bad := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(bad, []byte("faas_compute_sign_key_retired: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := retiredComputeKeys(bad, false); err == nil {
		t.Fatal("an unparseable vars file must be an error")
	}
}

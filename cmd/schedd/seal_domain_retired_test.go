package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeliveryRetiredFromEnv(t *testing.T) {
	contracted := t.TempDir()
	writeTestIdentity(t, filepath.Join(contracted, "host.age"))
	withFleet := t.TempDir()
	writeTestIdentity(t, filepath.Join(withFleet, "fleet.age"))
	writeTestIdentity(t, filepath.Join(withFleet, "host.age"))
	previousFleet := t.TempDir()
	writeTestIdentity(t, filepath.Join(previousFleet, "host.age"))
	if err := os.WriteFile(filepath.Join(previousFleet, "faas_fleet_age_identity_previous"), []byte("not even an identity"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		env       map[string]string
		viaSecret bool
		dir       string
		want      bool
		wantErr   string
	}{
		{name: "unset", env: map[string]string{}, dir: withFleet},
		{name: "false", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "false"}, dir: withFleet},
		{name: "retired", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "true"}, viaSecret: true, dir: contracted, want: true},
		{name: "bad value", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "1"}, viaSecret: true, dir: contracted, wantErr: "want true or false"},
		{name: "local opener", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "true"}, dir: contracted, wantErr: "FAAS_SCHEDD_DELIVERY_OPENER=secretd"},
		{name: "delivery identity left", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "true", "FAAS_SEAL_DOMAIN_DELIVERY_IDENTITY_PATH": "/run/credentials/x/faas_seal_domain_delivery"}, viaSecret: true, dir: contracted, wantErr: "remove the delivery identity"},
		{name: "fleet identity left", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "true"}, viaSecret: true, dir: withFleet, wantErr: "contract step"},
		{name: "previous fleet identity left", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RETIRED": "true"}, viaSecret: true, dir: previousFleet, wantErr: "contract step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deliveryRetiredFromEnv(func(k string) string { return tc.env[k] }, tc.viaSecret, tc.dir)
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

func writeTestIdentity(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(newSealDomainTestIdentity(t).String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

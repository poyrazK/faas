// adr: 388 — release-only managed migration credential delivery.

package state

import "testing"

func TestManagedPostgresSecretDeliveryPolicy(t *testing.T) {
	rows := []AppSecret{
		{Key: "TOKEN"},
		{Key: "DATABASE_URL", ManagedPostgresBindingID: "runtime", ManagedPostgresAccess: "read_write"},
		// Use an arbitrary key: access comes from the binding, not its name.
		{Key: "SCHEMA_DSN", ManagedPostgresBindingID: "ddl", ManagedPostgresAccess: "migration"},
	}
	for _, tc := range []struct {
		name      string
		requested map[string]string
		release   bool
		want      int
		denied    bool
	}{
		{name: "serving defaults", want: 2},
		{name: "release defaults", release: true, want: 3},
		{name: "serving allowlist", requested: map[string]string{"DATABASE_URL": "secret:DATABASE_URL"}, want: 1},
		{name: "release supplements serving allowlist", requested: map[string]string{"DATABASE_URL": "secret:DATABASE_URL"}, release: true, want: 2},
		{name: "explicit serving DDL grant", requested: map[string]string{"SCHEMA_DSN": "secret:SCHEMA_DSN"}, denied: true},
		{name: "missing release secret", requested: map[string]string{"MISSING": "secret:MISSING"}, release: true, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectAppSecretsForDelivery(rows, tc.requested, tc.release)
			if (err != nil) != tc.denied {
				t.Fatalf("delivery error = %v", err)
			}
			if tc.denied {
				return
			}
			if len(got) != tc.want {
				t.Fatalf("selected %d secrets, want %d", len(got), tc.want)
			}
			for _, row := range got {
				if row.ManagedPostgresAccess == "migration" && !tc.release {
					t.Fatal("serving received DDL")
				}
			}
		})
	}
	for _, access := range []string{"", "administrator"} {
		corrupt := []AppSecret{{Key: "SCHEMA_DSN", ManagedPostgresBindingID: "ddl", ManagedPostgresAccess: access}}
		if _, err := SelectAppSecretsForDelivery(corrupt, nil, true); err == nil {
			t.Fatal("missing/unknown binding metadata admitted")
		}
	}
}

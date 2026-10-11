//go:build !no_pg

package state

// adr: 797
import "testing"

func TestRegionalAppProvisionPostgresContract(t *testing.T) {
	s, pool, cell, a := releasePGFixture(t)
	regionalProvisionContract(t, s, s, cell, a)
	for _, sql := range []string{"UPDATE regional_app_provision_receipts SET app_slug='changed-app'", "DELETE FROM regional_app_provision_receipts", "TRUNCATE regional_app_provision_receipts"} {
		if _, err := pool.Exec(t.Context(), sql); err == nil {
			t.Fatalf("receipt guard allowed %s", sql)
		}
	}
}

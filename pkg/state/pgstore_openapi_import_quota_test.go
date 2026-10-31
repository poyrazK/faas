//go:build !no_pg

package state_test

import "testing"

// adr: 202610171904
func TestPgOpenAPIImportsShareTheCompanyAllowance(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	openAPIImportSubjectRaceContract(t, store, func(orgID string) {
		// Billing adoption is set by the provider confirmation path, which
		// this test does not drive.
		if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, orgID); err != nil {
			t.Fatal(err)
		}
	})
}

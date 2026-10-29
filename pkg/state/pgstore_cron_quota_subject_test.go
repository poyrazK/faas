//go:build !no_pg

package state_test

import (
	"fmt"
	"testing"
)

// adr: 202610171904
func TestPgEligibleCronsRankTheBillingSubject(t *testing.T) {
	for _, adopted := range []bool{true, false} {
		t.Run(fmt.Sprintf("adopted=%v", adopted), func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			eligibleCronsSubjectContract(t, store, adopted, func(orgID string) {
				// Billing adoption is set by the provider confirmation path,
				// which this test does not drive.
				if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, orgID); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

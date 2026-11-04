//go:build !no_pg

package state_test

import (
	"fmt"
	"testing"
)

// adr: 202610171904
func TestPgPerAccountQuotasCountTheBillingSubject(t *testing.T) {
	for _, kind := range quotaSubjectKinds() {
		for _, adopted := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/adopted=%v", kind.name, adopted), func(t *testing.T) {
				store, pool, ctx := pgStoreWithPool(t)
				quotaSubjectContract(t, store, kind, adopted, func(orgID string) {
					// Billing adoption is set by the provider confirmation
					// path, which this test does not drive.
					if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, orgID); err != nil {
						t.Fatal(err)
					}
				})
			})
		}
	}
}

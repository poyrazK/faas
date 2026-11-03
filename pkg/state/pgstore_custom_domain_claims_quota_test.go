//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 202610171904
func TestPgPendingDomainsShareTheCompanyBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scoped bool
	}{
		// The fleet-wide binding guard serializes every domain claim.
		{"fleet guard", false},
		// The scoped guard (ADR-20261027163626) locks only each claim's own
		// hostname, so only the budget's own lock serializes members' claims.
		{"scoped guard", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, pool, ctx := pgStoreWithPool(t)
			store := state.NewPgStore(pool, state.WithTrafficScopedBindingGuard(tc.scoped))
			pendingDomainSubjectRaceContract(t, store, func(orgID string) {
				// Billing adoption is set by the provider confirmation path,
				// which this test does not drive.
				if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, orgID); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// loadorg_wiring.go — helpers that bridge cmd/apid's server
// construction to pkg/authz. Lives alongside auth_facade.go so the
// LoadOrg seam is one place a future reader can audit. PR 4 of
// issue #190 / IAM-6 / ADR-061.
package apidserver

import (
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// maybeStoreBackedResolver returns a *authz.StoreBackedResolver when
// store is non-nil, or nil otherwise. The nil-tolerant shape lets
// newServerWithDeps skip the resolver on the handful of legacy
// tests that exercise degraded paths with no store wired
// (TestStatusJSONHandlerNoPrometheusURL and friends); s.loadOrg
// already treats a nil resolver as pass-through, so LoadOrg is
// inert in those tests — no DB call attempted, no panic.
//
// Production wiring always passes a real store, so the nil branch
// is test-only; production callers never observe nil.
func maybeStoreBackedResolver(store state.Store) *authz.StoreBackedResolver {
	if store == nil {
		return nil
	}
	return authz.NewStoreBackedResolver(store)
}

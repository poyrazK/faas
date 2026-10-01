//go:build !no_pg

// adr: 375
package state_test

import "testing"

func TestPgWarmPoolReconciliationCandidates(t *testing.T) {
	store, ctx, _ := pgWithPool(t)
	testWarmPoolReconciliationCandidates(t, store, resolveDefaultLocal(t, ctx, store))
}

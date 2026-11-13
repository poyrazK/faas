//go:build !no_pg

package state_test

import "testing"

func TestPgProjectReconcileTombstoneGuards(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testProjectReconcileTombstoneGuards(t, ctx, store)
}

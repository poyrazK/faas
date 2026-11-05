//go:build !no_pg

package state_test

import "testing"

func TestPgProjectReconcileResources(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testProjectReconcileResources(t, ctx, store)
}

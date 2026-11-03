//go:build !no_pg

package state_test

import "testing"

func TestPgProjectReconcileCreatesDraftVisibility(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testProjectReconcileCreatesDraftVisibility(t, ctx, store)
}

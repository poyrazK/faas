//go:build !no_pg

package state_test

import "testing"

func TestPgStoreProjectReconcileIgnoresPreviewApps(t *testing.T) {
	store, _ := pgStore(t)
	testProjectReconcileIgnoresPreviewApps(t, store)
}

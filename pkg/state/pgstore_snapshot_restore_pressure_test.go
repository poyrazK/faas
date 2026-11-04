//go:build !no_pg

package state_test

import (
	"testing"
)

func TestPgStoreSnapshotRestorePressureLeaseReleaseAndExpiry(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testSnapshotRestorePressureLeases(t, store, resolveDefaultLocal(t, ctx, store))
}

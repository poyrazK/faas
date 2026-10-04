//go:build !no_pg

// adr: 583
package state_test

import "testing"

func TestPgPromotionActivatesAndRestoresWorkloadSettings(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testPromotionWorkloadSettings(t, store, false)
}

func TestPgPromotionRollbackRejectsConcurrentWorkloadEdit(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testPromotionWorkloadSettings(t, store, true)
}

//go:build !no_pg

// adr: 375
package state_test

import "testing"

func TestPgCloneBucketReservationUsesFrozenCatalogueAndWorker(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneBucketReservationContract(t, s)
}

func TestPgCloneBucketReservationRejectsForeignTarget(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneBucketForeignTargetContract(t, s)
}

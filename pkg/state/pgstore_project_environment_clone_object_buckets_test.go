//go:build !no_pg

// adr: 590
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

// adr: 1139
func TestPgDurableCloneCapturesProjectSecrets(t *testing.T) {
	s, _, _ := pgWithPool(t)
	durableCloneCapturesProjectSecrets(t, s)
}

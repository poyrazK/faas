//go:build !no_pg

package state_test

import "testing"

func TestPgReservedAppsCountEnvironmentReservations(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	reservedEnvironmentContract(t, store)
}

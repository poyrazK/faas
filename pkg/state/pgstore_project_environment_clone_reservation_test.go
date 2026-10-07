//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentCloneReservationOwnership(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneReservationOwnership(t, s)
}

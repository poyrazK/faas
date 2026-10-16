//go:build !no_pg

// adr: 1342
package state_test

import "testing"

func TestPgFrozenThawGuards(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testFrozenThawGuards(t, store)
}

func TestPgFrozenThawSelectionAgreesWithWake(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testFrozenThawSelectionAgreesWithWake(t, store)
}

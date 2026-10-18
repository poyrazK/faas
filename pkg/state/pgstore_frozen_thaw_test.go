//go:build !no_pg

// adr: 1740
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

func TestPgAppRuntimeGeneration(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testAppRuntimeGeneration(t, store)
}

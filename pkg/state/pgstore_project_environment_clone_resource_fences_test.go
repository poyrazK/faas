//go:build !no_pg

// adr: 590
package state_test

import "testing"

func TestPgCloneResourceInventoryFencesPublication(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testCloneResourceInventoryFences(t, store)
}

//go:build !no_pg

// adr: 20261102124306
package state_test

import "testing"

func TestPgAppMemoryPressure(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testAppMemoryPressure(t, store)
}

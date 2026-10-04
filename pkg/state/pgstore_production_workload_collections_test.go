//go:build !no_pg

// adr: 566
package state_test

import "testing"

func TestPgProductionAppEditPreservesWorkloadCollections(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testProductionAppEditPreservesWorkloadCollections(t, store)
}

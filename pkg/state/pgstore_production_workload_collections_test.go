//go:build !no_pg

// adr: 568
package state_test

import "testing"

func TestPgProductionAppEditPreservesWorkloadCollections(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testProductionAppEditPreservesWorkloadCollections(t, store)
}

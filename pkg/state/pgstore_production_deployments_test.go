//go:build !no_pg

// adr: 568
package state_test

import "testing"

func TestPgProductionDeploymentSelection(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testProductionDeploymentSelection(t, store)
}

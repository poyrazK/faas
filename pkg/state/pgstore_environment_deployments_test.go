//go:build !no_pg

// adr: 568
package state_test

import "testing"

func TestPgEnvironmentDeploymentSelection(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentDeploymentSelection(t, store)
}

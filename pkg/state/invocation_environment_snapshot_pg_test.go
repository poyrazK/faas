//go:build !no_pg

// adr: 570, 590
package state_test

import "testing"

func TestPgInvocationEnvironmentSnapshotDispatch(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testInvocationEnvironmentSnapshotDispatch(t, store)
}

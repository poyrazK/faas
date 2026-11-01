//go:build !no_pg

// adr: 20261101091815
package state_test

import "testing"

func TestPgNodeMemoryReports(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testNodeMemoryReports(t, store)
}

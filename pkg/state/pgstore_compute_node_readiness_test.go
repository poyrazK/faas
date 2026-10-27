//go:build !no_pg

// adr: 20261022184531
package state_test

import "testing"

func TestPgComputeNodeReadiness(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testComputeNodeReadiness(t, store)
}

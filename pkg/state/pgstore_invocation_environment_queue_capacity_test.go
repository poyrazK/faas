//go:build !no_pg

// adr: 568
package state_test

import "testing"

func TestPgEnvironmentQueueProducerDepthIsAtomicAndIsolated(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueProducerDepth(t, store)
}

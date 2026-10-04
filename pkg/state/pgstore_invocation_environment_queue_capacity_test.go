//go:build !no_pg

// adr: 581
package state_test

import "testing"

func TestPgEnvironmentQueueProducerDepthIsAtomicAndIsolated(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueProducerDepth(t, store)
}

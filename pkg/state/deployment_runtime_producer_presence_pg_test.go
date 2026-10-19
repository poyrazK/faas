//go:build !no_pg

package state

// adr: 435. Real scoped PostgreSQL history, including invalid current metadata.

import "testing"

func TestPgRuntimeProducerPresenceIncludesStaleHistory(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeProducerPresence(t, s)
}

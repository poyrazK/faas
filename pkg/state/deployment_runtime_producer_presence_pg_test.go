//go:build !no_pg

package state_test

// adr: 435. Real scoped PostgreSQL history, including invalid current metadata.

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgRuntimeProducerPresenceIncludesStaleHistory(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	runtimeProducerPresence(t, s)
}

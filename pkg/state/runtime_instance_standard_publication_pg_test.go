//go:build !no_pg

package state_test

// adr: 595. PostgreSQL commits native authority and owned config together.

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgOwnedApplicationStandardRuntimePublication(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	ownedStandardRuntimePublication(t, s)
}

func TestPgOwnedApplicationStandardWarmPromotion(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	ownedStandardWarmPromotion(t, s)
}

//go:build !no_pg

package state

// adr: 595. PostgreSQL commits native authority and owned config together.

import "testing"

func TestPgOwnedApplicationStandardRuntimePublication(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	ownedStandardRuntimePublication(t, s)
}

func TestPgOwnedApplicationStandardWarmPromotion(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	ownedStandardWarmPromotion(t, s)
}

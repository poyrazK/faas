//go:build !no_pg

package state

// adr: 429/422 — standards compose with ordinary lifecycle and capacity checks.

import "testing"

func TestPgInstanceApplicationStandardUnmanagedPlanCompatibility(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardUnmanagedPlanCompatibility(t, s)
}

func TestPgInstanceApplicationStandardManagedPlanStaysStrict(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardManagedPlanStaysStrict(t, s)
}

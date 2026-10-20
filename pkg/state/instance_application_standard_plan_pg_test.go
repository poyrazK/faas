//go:build !no_pg

package state_test

// adr: 435/422 — standards compose with ordinary lifecycle and capacity checks.

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgInstanceApplicationStandardUnmanagedPlanCompatibility(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardUnmanagedPlanCompatibility(t, s)
}

func TestPgInstanceApplicationStandardManagedPlanStaysStrict(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardManagedPlanStaysStrict(t, s)
}

//go:build !no_pg

// adr: 20261027220445
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Both admission paths check the node's vCPU and CPU budgets the same way.
func TestNodeComputeReservationPostgres(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	if !state.AdmissionFunctionsUsable(t.Context(), store) {
		t.Fatal("migrated database lacks the admission functions")
	}
	t.Run("function", func(t *testing.T) { checkNodeComputeReservation(t, store) })
	t.Run("statements", func(t *testing.T) { checkNodeComputeReservation(t, state.StatementAdmissionStore(store)) })
}

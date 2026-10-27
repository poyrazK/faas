//go:build !no_pg

// adr: 20261027172037
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSnapshottingHoldsNodeRAMPostgres(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	if !state.AdmissionFunctionsUsable(t.Context(), store) {
		t.Fatal("migrated database lacks the admission functions")
	}
	t.Run("function", func(t *testing.T) { checkSnapshottingHoldsNodeRAM(t, store) })
	t.Run("statements", func(t *testing.T) { checkSnapshottingHoldsNodeRAM(t, state.StatementAdmissionStore(store)) })
}

//go:build !no_pg

// ADR-1707: verified workspace onboarding, retained setup and activation evidence.
package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlOnboardingPostgres(t *testing.T) {
	s := state.XNewPgStore(t)
	write := func(op state.Operation, settle bool) {
		t.Helper()
		tx, err := (*s.Pool()).Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if settle {
			err = state.SettleProductOperationTx(t.Context(), tx, op)
		} else {
			err = state.InsertProductOperationTx(t.Context(), tx, op)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	retireAuth := func(id string) {
		t.Helper()
		if _, err := (*s.Pool()).Exec(t.Context(), "DELETE FROM apps WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	productOnboardingContract(t, s, func(op state.Operation) { write(op, false) }, func(op state.Operation) { write(op, true) }, retireAuth)
}

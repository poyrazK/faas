//go:build !no_pg

// ADR-1597: verified workspace onboarding, retained setup and activation evidence.
package state

import "testing"

func TestProductControlOnboardingPostgres(t *testing.T) {
	s := newPgStore(t)
	write := func(op Operation, settle bool) {
		t.Helper()
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if settle {
			err = settleProductOperationTx(t.Context(), tx, op)
		} else {
			err = insertProductOperationTx(t.Context(), tx, op)
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
		if _, err := s.pool.Exec(t.Context(), "DELETE FROM apps WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	productOnboardingContract(t, s, func(op Operation) { write(op, false) }, func(op Operation) { write(op, true) }, retireAuth)
}

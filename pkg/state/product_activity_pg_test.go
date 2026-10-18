//go:build !no_pg

package state

import "testing"

func TestProductControlActivityPostgres(t *testing.T) {
	s := newPgStore(t)
	insert := func(op Operation) {
		t.Helper()
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err = insertProductOperationTx(t.Context(), tx, op); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	productActivityContract(t, s, productBillingPGFixture(t, s), insert)
}

//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlActivityPostgres(t *testing.T) {
	s := state.XNewPgStore(t)
	insert := func(op state.Operation) {
		t.Helper()
		tx, err := (*s.Pool()).Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err = state.InsertProductOperationTx(t.Context(), tx, op); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	state.ProductActivityContract(t, s, state.ProductBillingPGFixture(t, s), insert)
}

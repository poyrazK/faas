//go:build !no_pg

package state_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlRunDetailsPostgres(t *testing.T) {
	s := state.XNewPgStore(t)
	state.ProductRunDetailsContract(t, s, state.ProductBillingPGFixture(t, s))
}

func TestProductControlRunDetailsLivePostgres(t *testing.T) {
	s := state.XNewPgStore(t)
	persist := func(op state.Operation, settled bool) {
		t.Helper()
		record, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := (*s.Pool()).Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if _, err = tx.Exec(t.Context(), `UPDATE customer_operations SET state=$2,record=$3,expires_at=$4 WHERE id=$1`, op.ID, op.State, record, op.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		if settled {
			if err = state.SettleProductOperationTx(t.Context(), tx, op); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	state.ProductRunDetailsLiveContract(t, s, persist)
}

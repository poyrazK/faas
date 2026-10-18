//go:build !no_pg

package state

import (
	"encoding/json"
	"testing"
)

func TestProductControlRunDetailsPostgres(t *testing.T) {
	s := newPgStore(t)
	productRunDetailsContract(t, s, productBillingPGFixture(t, s))
}

func TestProductControlRunDetailsLivePostgres(t *testing.T) {
	s := newPgStore(t)
	persist := func(op Operation, settled bool) {
		t.Helper()
		record, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if _, err = tx.Exec(t.Context(), `UPDATE customer_operations SET state=$2,record=$3,expires_at=$4 WHERE id=$1`, op.ID, op.State, record, op.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		if settled {
			if err = settleProductOperationTx(t.Context(), tx, op); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	productRunDetailsLiveContract(t, s, persist)
}

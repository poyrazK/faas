//go:build !no_pg

package state

import (
	"encoding/json"
	"testing"
)

func TestProductControlWorkspaceActivityPostgres(t *testing.T) {
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
	productWorkspaceActivityContract(t, s, productBillingPGFixture(t, s), insert)
}

func TestProductControlWorkspaceActivityRemappingPostgres(t *testing.T) {
	productWorkspaceActivityRemapping(t, newPgStore(t))
}

func TestProductControlWorkspaceActivityAttentionPostgres(t *testing.T) {
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
	productWorkspaceActivityAttention(t, s, persist)
}

package datarepair

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepairRejectsReplicaExecutionAndRetainsReceiptReplay(t *testing.T) {
	e, scope, source := fixture(t, 1)
	p := approved(t, e, scope, source)
	ctx := t.Context()
	cfg := e.Pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["session_replication_role"] = "replica"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	replica := &Engine{Pool: pool}
	before := validationState(t, e, p.ID)
	repairID := uuid.NewString()
	if _, err := replica.Apply(ctx, scope, p.ID, p.Digest, repairID, "replica"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replica session applied a correction: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = Capture(ctx, tx, scope, uuid.NewString(), "replica", "products", func(pgx.Tx) error { called = true; return nil })
	_ = tx.Rollback(ctx)
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("replica capture reached business code: called=%v err=%v", called, err)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("rejected replica execution changed rows, versions, journals or receipts")
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, repairID, "origin"); err != nil {
		t.Fatal(err)
	}
	// Read-only recovery of an existing receipt does not require write readiness.
	if receipt, err := replica.Apply(ctx, scope, p.ID, p.Digest, repairID, "replay"); err != nil || receipt.AppliedAt == nil || *receipt.RepairOperationID != repairID {
		t.Fatalf("session qualification blocked durable receipt recovery: %+v %v", receipt, err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("origin apply lost its journal: %+v %v", r, err)
	}
}

func TestRepairRejectsReplicaModeBeforeFinalization(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	err = Capture(ctx, tx, scope, id, "changed-session", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SET LOCAL session_replication_role='replica'; UPDATE products SET category='original'`)
		return err
	})
	_ = tx.Rollback(ctx)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("capture finalized with disabled triggers: %v", err)
	}
	var intact bool
	if err := e.Pool.QueryRow(ctx, `SELECT category='bad' AND gregale_row_version=2 AND NOT EXISTS(SELECT 1 FROM gregale_repair.operations WHERE id=$1) FROM products`, id).Scan(&intact); err != nil || !intact {
		t.Fatalf("failed finalization retained unjournaled changes: %v %v", intact, err)
	}
}

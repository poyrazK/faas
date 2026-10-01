package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectCapacityDatabaseFencesAndRecovery(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	token := uuid.NewString()
	if err := st.BeginObjectWrite(ctx, b.AccountID, b.ID, token, "tracked", 10, p); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleObjectWrite(ctx, b.AccountID, b.ID, token); err != nil {
		t.Fatal(err)
	}
	j, err := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "old")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE object_storage_key_grants SET max_bytes=max_bytes+1 WHERE bucket_id=$1`,
		`UPDATE object_buckets SET state='deleting' WHERE id=$1`,
	} {
		_, err = pool.Exec(ctx, query, b.ID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatalf("old replica bypassed fence: %v", err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET lease_until=now()-interval '1 second',retry_at=now() WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "new")
	if err != nil || j.State != "scanning" {
		t.Fatal(j, err)
	}
	if _, err = st.FinishObjectCapacityReconciliation(ctx, j.ID, "old", 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired worker refunded quota", err)
	}
	if _, err = st.CancelObjectCapacityReconciliation(ctx, b.AccountID, b.ID, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishObjectCapacityReconciliation(ctx, j.ID, "new", 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cancelled worker refunded quota", err)
	}
	// Old SQL upserts leave the tracking ID unchanged; the trigger downgrades it.
	if _, err = pool.Exec(ctx, `UPDATE object_storage_key_grants SET max_bytes=max_bytes WHERE bucket_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	j, err = st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "blocked")
	if err != nil || j.State != "blocked" {
		t.Fatal(j, err)
	}
	// An uncertain write remains pending even after the job's deadline expires.
	b, _ = seedAccounting(t, st)
	token = uuid.NewString()
	if err = st.BeginObjectWrite(ctx, b.AccountID, b.ID, token, "uncertain", 10, p); err != nil {
		t.Fatal(err)
	}
	j, err = st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET deadline_at=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "deadline")
	if err != nil || j.State != "failed" || j.LastErrorCode != "deadline" || j.PendingWrites != 1 || j.ReclaimedBytes != 0 {
		t.Fatal(j, err)
	}
}

func TestObjectCapacityRequestDoesNotDeadlockAdmission(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, b.AccountID); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, e := st.RequestObjectCapacityReconciliation(requestCtx, b.AccountID, b.AppID, b.ID)
		result <- e
	}()
	// The private test database has no other blocked sessions. Wait until the
	// request holds its bucket lock and waits for our account lock.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not wait for account lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The FK takes KEY SHARE on the bucket. A FOR UPDATE bucket lock here
	// would deadlock against our account lock; NO KEY UPDATE permits progress.
	if _, err = tx.Exec(requestCtx, `INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) VALUES($1,repeat('d',64),1)`, b.ID); err != nil {
		t.Fatal("admission deadlocked with reconciliation", err)
	}
	if err = tx.Commit(requestCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}

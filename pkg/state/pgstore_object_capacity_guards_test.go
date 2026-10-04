package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
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
	objectMutationDoesNotDeadlockAdmission(t, func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
		_, err := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		return err
	})
}

// adr: 564 — every bucket mutation follows admission's account/SHARE order.
func TestObjectBucketMutationRequestsDoNotDeadlockAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *state.PgStore, state.ObjectBucket) error
	}{
		{"encryption", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, err := st.RequestObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, state.ObjectEncryptionSnapshot{})
			return err
		}},
		{"lifecycle", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, err := st.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, nil)
			return err
		}},
		{"notifications", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, err := st.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, nil)
			return err
		}},
		{"deletion", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, _, err := st.BeginObjectDeletion(ctx, state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "tracked"}, AccountID: b.AccountID, AppID: b.AppID, Token: "contention"}, accountingPolicy())
			return err
		}},
		{"multipart", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, err := st.ReserveAdmittedObjectMultipartUpload(ctx, fixedMultipartCandidate(b, "lock order"), 100, accountingPolicy())
			return err
		}},
		{"versioning", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, err := st.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled")
			return err
		}},
		{"object lock", func(ctx context.Context, st *state.PgStore, b state.ObjectBucket) error {
			_, err := st.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, api.ObjectBucketObjectLockConfiguration{Enabled: true})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { objectMutationDoesNotDeadlockAdmission(t, tc.run) })
	}
}

func objectMutationDoesNotDeadlockAdmission(t *testing.T, request func(context.Context, *state.PgStore, state.ObjectBucket) error) {
	t.Helper()
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
		result <- request(requestCtx, st, b)
	}()
	// The request must wait for our account before locking the bucket. The
	// Object Lock admission trigger needs SHARE, beyond the FK's KEY SHARE.
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
	// Admission must obtain its bucket SHARE fence while owning the account.
	if _, err = tx.Exec(requestCtx, `INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) VALUES($1,repeat('d',64),1)`, b.ID); err != nil {
		t.Fatal("admission deadlocked with bucket mutation", err)
	}
	// Keep the admission's SHARE lock through commit without leaving an unsafe
	// direct grant that would correctly prevent versioning/Object Lock cutover.
	if _, err = tx.Exec(requestCtx, `DELETE FROM object_storage_key_grants WHERE bucket_id=$1 AND key_hash=repeat('d',64)`, b.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(requestCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}

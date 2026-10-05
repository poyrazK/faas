package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectBucketWriteCleanupFenceMem(t *testing.T) {
	objectBucketWriteCleanupSuite(t, state.NewMemStore())
}
func TestObjectBucketWriteCleanupFencePG(t *testing.T) {
	s, _ := pgStore(t)
	objectBucketWriteCleanupSuite(t, s)
}

func objectBucketWriteCleanupSuite(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	writes := st.(state.ObjectCapacityStore)
	id := uuid.NewString()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := writes.BeginObjectWrite(ctx, b.AccountID, b.ID, id, "proof", 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "cleanup", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unsettled write did not fence bucket cleanup", err)
	}
	if err := writes.SettleObjectWrite(ctx, b.AccountID, b.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "cleanup", "deleting"); err != nil {
		t.Fatal("settled write still blocked cleanup", err)
	}
	if err := writes.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "late", 1, accountingPolicy()); err == nil {
		t.Fatal("new writer crossed cleanup fence")
	}

	// Race account-serialized admission with the bucket claim. Exactly one may
	// win; cleanup must not claim after admission or deadlock holding the bucket.
	for range 10 {
		b, _ := seedAccounting(t, st)
		start := make(chan struct{})
		var writeErr, claimErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			writeErr = writes.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "proof", 1, accountingPolicy())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, claimErr = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "cleanup", "deleting")
		}()
		close(start)
		wg.Wait()
		if (writeErr == nil) == (claimErr == nil) || ctx.Err() != nil {
			t.Fatalf("admission/cleanup race: write=%v claim=%v context=%v", writeErr, claimErr, ctx.Err())
		}
	}
}

func TestObjectBucketWriteCleanupDatabaseGuard(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	id := uuid.NewString()
	if err := st.BeginObjectWrite(ctx, b.AccountID, b.ID, id, "proof", 10, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE object_buckets SET state='deleting' WHERE id=$1`,
		`UPDATE object_buckets SET state='deleted' WHERE id=$1`,
		`DELETE FROM object_buckets WHERE id=$1`,
	} {
		_, err := pool.Exec(ctx, query, b.ID)
		var check *pgconn.PgError
		if !errors.As(err, &check) || check.ConstraintName != "object_bucket_pending_write_fenced" {
			t.Fatalf("old replica removed accepted write evidence: %v", err)
		}
	}
	if err := st.SettleObjectWrite(ctx, b.AccountID, b.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "cleanup", "deleting"); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO object_storage_write_admissions(id,bucket_id,key_hash,kind) VALUES($1,$2,$3,'proxy')`, uuid.NewString(), b.ID, strings.Repeat("a", 64))
	var check *pgconn.PgError
	if !errors.As(err, &check) || check.ConstraintName != "object_bucket_pending_write_fenced" {
		t.Fatal("old replica admitted a write during cleanup", err)
	}
}

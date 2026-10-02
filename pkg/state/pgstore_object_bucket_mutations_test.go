//go:build !no_pg

// adr:375
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// ObjectBucketClaim must evaluate fences after its source-row lock wait. A
// single UPDATE's initial snapshot would otherwise miss a newly committed
// fence and delete a source whose coordinator has already paused its writers.
func TestObjectBucketMutationPGLifecycleLockWaitSeesCommittedFence(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	b := mutationBucketFixture(t, s)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "select id from object_buckets where id=$1 for update", b.ID); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.ClaimObjectBucket(claimCtx, b.AccountID, b.AppID, b.ID, uuid.NewString(), "deleting")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%ObjectBucketMutationLock%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("claim did not wait for source lock: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("claim never blocked on the source lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.Exec(ctx, "insert into object_bucket_write_fences(bucket_id,token,backend_id,backend_fingerprint,physical_name) values($1,$2,$3,$4,$5)", b.ID, uuid.NewString(), b.BackendID, b.BackendFingerprint, b.PhysicalName); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("claim ignored the fence committed during its lock wait: %v", err)
	}
	live, err := s.GetObjectBucket(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || live.State != "ready" {
		t.Fatalf("source changed despite active fence: %+v %v", live, err)
	}
}

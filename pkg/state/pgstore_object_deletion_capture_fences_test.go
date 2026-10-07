//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

const olderReplicaDeletionInsert = `INSERT INTO object_deletions(id,bucket_id,object_key,state) VALUES($1,$2,'key','prepared')`

func waitDeletionCaptureSQLLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND strpos(query,$1)>0)`, query).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("deletion/capture did not wait at %s", query)
}

func TestPgObjectDeletionCaptureAdmissionLockWaitSeesCommittedHold(t *testing.T) {
	for _, replica := range []string{"current", "older"} {
		t.Run(replica, func(t *testing.T) {
			s, pool, ctx := pgStoreWithPool(t)
			b, _ := seedAccounting(t, s)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := tx.Exec(ctx, "select id from object_buckets where id=$1 for update", b.ID); err != nil {
				t.Fatal(err)
			}
			admitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			input := deletionCaptureInput(b)
			query := "ObjectCapacityLockBucket"
			if replica == "older" {
				query = olderReplicaDeletionInsert
			}
			go func() {
				var err error
				if replica == "older" {
					_, err = pool.Exec(admitCtx, olderReplicaDeletionInsert, input.ID, b.ID)
				} else {
					_, _, err = s.BeginObjectDeletion(admitCtx, input, accountingPolicy())
				}
				done <- err
			}()
			waitDeletionCaptureSQLLock(t, ctx, pool, query)
			if _, err := tx.Exec(ctx, `insert into object_bucket_write_fences(bucket_id,token,backend_id,backend_fingerprint,physical_name) values($1,$2,$3,$4,$5)`, b.ID, uuid.NewString(), b.BackendID, b.BackendFingerprint, b.PhysicalName); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if replica == "older" {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "55000" || pgErr.ConstraintName != "object_deletion_capture_fenced" {
					t.Fatalf("older replica ignored hold committed during lock wait: %v", err)
				}
			} else if !errors.Is(err, state.ErrObjectBucketWriteFenced) {
				t.Fatalf("admission ignored hold committed during lock wait: %v", err)
			}
			if _, err := s.GetObjectDeletion(ctx, b.AccountID, b.ID, input.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("fenced admission retained an intent: %v", err)
			}
		})
	}
}

func TestPgObjectDeletionCaptureCountsAdmissionAfterLockWait(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// The insertion trigger also serializes replicas that do not know about
	// capture. Acquisition must count their intent after its source lock wait.
	if _, err := tx.Exec(ctx, olderReplicaDeletionInsert, uuid.NewString(), b.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		fence state.ObjectBucketWriteFence
		err   error
	}
	done := make(chan result, 1)
	go func() {
		fence, err := s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
		done <- result{fence, err}
	}()
	waitDeletionCaptureSQLLock(t, ctx, pool, "ObjectBucketMutationLock")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.fence.Deletions != 1 || got.fence.Requests != 0 {
		t.Fatalf("acquisition missed admitted older-replica deletion: %+v %v", got.fence, got.err)
	}
}

func TestPgObjectDeletionCaptureAdmissionRejectsOldSnapshots(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			s, pool, ctx := pgStoreWithPool(t)
			b, _ := seedAccounting(t, s)
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			var holds int
			if err := tx.QueryRow(ctx, "select count(*) from object_bucket_write_fences").Scan(&holds); err != nil || holds != 0 {
				t.Fatalf("original snapshot: %d %v", holds, err)
			}
			if _, err := s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, olderReplicaDeletionInsert, uuid.NewString(), b.ID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "object_deletion_admission_isolation" {
				t.Fatalf("old transaction snapshot could hide source hold: %v", err)
			}
		})
	}
}

func TestPgObjectDeletionCaptureMigrationRoundTripAndHeldDownRefusal(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	raw, err := migrations.FS.ReadFile("20261006161300941_object_deletion_capture_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing deletion admission downgrade")
	}
	const shape = `SELECT jsonb_build_array(
 (SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='fence_object_deletion_capture_admission'),
 (SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_deletion_capture_admission' AND tgrelid='object_deletions'::regclass))::text`
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var before, after string
	if err := tx.QueryRow(ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.QueryRow(ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatalf("migration round trip changed admission guard: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	fence, err := s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, parts[1])
	_ = tx.Rollback(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "object_deletion_capture_admission_down_held" {
		t.Fatalf("downgrade removed admission enforcement with owned hold: %v", err)
	}
	if _, err := pool.Exec(ctx, olderReplicaDeletionInsert, uuid.NewString(), b.ID); err == nil {
		t.Fatal("failed downgrade left source admission unguarded")
	}
	if err := s.ReleaseObjectBucketWriteFence(ctx, b, fence.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, olderReplicaDeletionInsert, uuid.NewString(), b.ID); err != nil {
		t.Fatalf("guard did not reopen released source: %v", err)
	}
}

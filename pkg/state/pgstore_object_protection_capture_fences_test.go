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
	"github.com/onebox-faas/faas/migrations"
)

func TestPgObjectProtectionCaptureMigrationRoundTripAndHeldDownRefusal(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	raw, err := migrations.FS.ReadFile("20261006170801000_object_protection_capture_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing protection admission downgrade")
	}
	const shape = `SELECT jsonb_build_array(
 (SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='fence_object_protection_capture_admission'),
 (SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_clone_configuration_mutation'),
 (SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_protection_capture_admission' AND tgrelid='object_version_protection'::regclass))::text`
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
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "object_protection_capture_admission_down_held" {
		t.Fatalf("downgrade removed admission enforcement with owned hold: %v", err)
	}
	if err := s.ReleaseObjectBucketWriteFence(ctx, b, fence.Token); err != nil {
		t.Fatal(err)
	}
}

// An older replica has no Go admission check. The database trigger must use
// the snapshot after its source-row wait, before any provider dispatch.
func TestPgObjectProtectionCaptureOlderAdmissionWait(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	const insert = `INSERT INTO object_version_protection(id,bucket_id,account_id,app_id,object_key,public_version_id,native_version_id,intent) VALUES($1,$2,$3,$4,'key','null','null','{}')`
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM object_buckets WHERE id=$1 FOR UPDATE`, b.ID); err != nil {
		t.Fatal(err)
	}
	admitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := pool.Exec(admitCtx, insert, uuid.NewString(), b.ID, b.AccountID, b.AppID)
		done <- err
	}()
	waitDeletionCaptureSQLLock(t, ctx, pool, insert)
	if _, err := tx.Exec(ctx, `INSERT INTO object_bucket_write_fences(bucket_id,token,backend_id,backend_fingerprint,physical_name) VALUES($1,$2,$3,$4,$5)`, b.ID, uuid.NewString(), b.BackendID, b.BackendFingerprint, b.PhysicalName); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var pgErr *pgconn.PgError
	if err := <-done; !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_protection_capture_fenced" {
		t.Fatalf("older admission missed committed hold: %v", err)
	}
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, insert, uuid.NewString(), b.ID, b.AccountID, b.AppID)
		_ = tx.Rollback(ctx)
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_protection_admission_isolation" {
			t.Fatalf("old snapshot allowed admission: %v", err)
		}
	}
}

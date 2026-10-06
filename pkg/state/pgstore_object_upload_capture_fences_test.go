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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
)

func TestPgObjectUploadCaptureMigrationRoundTripAndHeldDownRefusal(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	raw, err := migrations.FS.ReadFile("20261006174259000_object_upload_capture_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing upload admission downgrade")
	}
	const shape = `SELECT jsonb_build_array(
 (SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='fence_object_upload_capture_admission'),
 (SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_multipart_capture_admission' AND tgrelid='object_storage_multipart_uploads'::regclass),
 (SELECT pg_get_indexdef('object_upload_capture_pending_idx'::regclass)),
 (SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_upload_capture_admission' AND tgrelid='object_upload_completions'::regclass))::text`
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
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "object_upload_capture_admission_down_held" {
		t.Fatalf("downgrade removed admission enforcement with owned hold: %v", err)
	}
	if err := s.ReleaseObjectBucketWriteFence(ctx, b, fence.Token); err != nil {
		t.Fatal(err)
	}
}

func TestPgObjectUploadCaptureRejectsOlderReplicaAdmission(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	f, err := s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"object_upload_completions", "object_storage_multipart_uploads"} {
		_, err := pool.Exec(ctx, "INSERT INTO "+table+"(id,account_id,app_id,bucket_id) VALUES($1,$2,$3,$4)", uuid.NewString(), b.AccountID, b.AppID, b.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55000" || pgErr.ConstraintName != "object_upload_capture_fenced" {
			t.Fatalf("%s older replica escaped: %v", table, err)
		}
	}
	if err := s.ReleaseObjectBucketWriteFence(ctx, b, f.Token); err != nil {
		t.Fatal(err)
	}
	for _, isolation := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		for _, table := range []string{"object_upload_completions", "object_storage_multipart_uploads"} {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL "+isolation); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, "INSERT INTO "+table+"(id,account_id,app_id,bucket_id) VALUES($1,$2,$3,$4)", uuid.NewString(), b.AccountID, b.AppID, b.ID)
			_ = tx.Rollback(ctx)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_upload_admission_isolation" {
				t.Fatalf("%s %s admission: %v", table, isolation, err)
			}
		}
	}
}

func TestPgObjectUploadCaptureOlderAdmissionWait(t *testing.T) {
	for _, table := range []string{"object_upload_completions", "object_storage_multipart_uploads"} {
		t.Run(table, func(t *testing.T) {
			s, pool, ctx := pgStoreWithPool(t)
			b, _ := seedAccounting(t, s)
			insert := "INSERT INTO " + table + "(id,account_id,app_id,bucket_id) VALUES($1,$2,$3,$4)"
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
				_, err := pool.Exec(admitCtx, insert, uuid.NewString(), b.AccountID, b.AppID, b.ID)
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
			if err := <-done; !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_upload_capture_fenced" {
				t.Fatalf("older admission missed committed hold: %v", err)
			}
		})
	}
}

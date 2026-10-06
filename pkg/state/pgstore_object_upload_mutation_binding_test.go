//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgObjectUploadMutationBindingGuardsAndRollback(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, s)
	route, err := s.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "binding", MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := s.BeginTrackedObjectUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "key", Bytes: 10, Status: "pending"}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM object_bucket_mutations WHERE upload_id=$1`, `UPDATE object_bucket_mutations SET physical_name='different' WHERE upload_id=$1`, `UPDATE object_upload_completions SET object_key='different' WHERE id=$1`} {
		_, err := pool.Exec(ctx, query, c.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("unsettled receipt guard: %v", err)
		}
	}
	raw, err := migrations.FS.ReadFile("20261006181423000_object_upload_mutation_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, parts[1])
	_ = tx.Rollback(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "object_upload_mutation_down_busy" {
		t.Fatalf("downgrade discarded owned upload: %v", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE object_upload_completions SET status='failed',error_code='dispatch_failed',write_phase='settled' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM object_bucket_mutations WHERE upload_id=$1`, c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("terminal journal did not retire receipt atomically", count, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadTrackedObjectUploadMutation(ctx, c); err != nil {
		t.Fatal("rolled-back settlement erased original", err)
	}
	c.Status, c.ErrorCode = "failed", "dispatch_failed"
	if _, err := s.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var before, after string
	const shape = `SELECT jsonb_build_array((SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_bound_upload_mutation'),(SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='compose_object_upload_mutation'),(SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='protect_bound_upload_journal'),(SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_upload_mutation_composition'),(SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_upload_mutation_guard'))::text`
	if err := tx.QueryRow(ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	latest, err := migrations.FS.ReadFile("20261006221829000_object_multipart_mutation_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	latestParts := strings.Split(string(latest), "-- +goose Down")
	if _, err := tx.Exec(ctx, latestParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, latestParts[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatal("binding migration round trip changed guard", err)
	}
}

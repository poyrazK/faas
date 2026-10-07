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
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgObjectMultipartMutationBindingGuardsAndRollback(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	u, err := st.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "original", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := st.ActivateObjectMultipartUpload(ctx, u.ID, "init", "native-original"); err != nil {
		t.Fatal(err)
	}
	u, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := st.ReadObjectMultipartMutation(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadObjectMultipartMutation(ctx, u); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired lease resumed original provider operation", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET lease_until=$2 WHERE id=$1`, u.ID, u.LeaseUntil); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM object_bucket_mutations WHERE multipart_upload_id=$1`, `UPDATE object_bucket_mutations SET physical_name='changed' WHERE multipart_upload_id=$1`, `UPDATE object_storage_multipart_uploads SET object_key='changed' WHERE id=$1`, `UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '1 day' WHERE id=$1`, `UPDATE object_storage_multipart_uploads SET provider_upload_id='changed' WHERE id=$1`} {
		_, err := pool.Exec(ctx, query, u.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatal("bound intent rewrite escaped", query, err)
		}
	}
	raw, err := migrations.FS.ReadFile("20261006221829000_object_multipart_mutation_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade removed live session custody")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='aborted',lease_token=NULL,lease_until=NULL WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM object_bucket_mutations WHERE multipart_upload_id=$1`, u.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("terminal session did not retire original receipt", count, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := st.ReadObjectMultipartMutation(ctx, u)
	if err != nil || restored.ID != receipt.ID {
		t.Fatal("rollback lost receipt", restored, err)
	}
	if err := st.FinishVerifiedObjectMultipartAbort(ctx, u.ID, u.LeaseToken); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	const shape = `SELECT jsonb_build_array((SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_bound_multipart_mutation'),(SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='compose_object_multipart_mutation'),(SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='protect_bound_multipart_journal'),(SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_multipart_mutation_composition'))::text`
	var before, after string
	if err := tx.QueryRow(ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	initiation, err := migrations.FS.ReadFile("20261007074655000_object_multipart_initiation_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	initiationParts := strings.SplitN(string(initiation), "-- +goose Down", 2)
	if _, err := tx.Exec(ctx, initiationParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, initiationParts[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatal("migration round trip changed binding", err)
	}
}

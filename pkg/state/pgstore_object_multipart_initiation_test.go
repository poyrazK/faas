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

func TestPgObjectMultipartInitiationGuardsRollbackAndRestart(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	u, err := st.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "original", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	u, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "original", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE object_multipart_initiation_dispatches SET dispatched=true,dispatch_token='original' WHERE multipart_upload_id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if d, err := st.ReadObjectMultipartInitiation(ctx, u); err != nil || d.Dispatched {
		t.Fatal("rollback committed dispatch", d, err)
	}
	if err := st.DispatchObjectMultipartInitiation(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`DELETE FROM object_multipart_initiation_dispatches WHERE multipart_upload_id=$1`,
		`UPDATE object_multipart_initiation_dispatches SET dispatched=false,dispatch_token='' WHERE multipart_upload_id=$1`,
		`UPDATE object_multipart_initiation_dispatches SET dispatch_token='replacement' WHERE multipart_upload_id=$1`,
		`UPDATE object_storage_multipart_uploads SET state='active',provider_upload_id='unobserved' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET state='aborted' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET size_bytes=size_bytes+1 WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET part_count=part_count+1 WHERE id=$1`,
	} {
		_, err := pool.Exec(ctx, query, u.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatal("dispatch evidence rewrite escaped", query, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.ObserveObjectMultipartInitiation(ctx, u, "native"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired owner recorded reply", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET lease_until=$2 WHERE id=$1`, u.ID, u.LeaseUntil); err != nil {
		t.Fatal(err)
	}
	if err := st.ObserveObjectMultipartInitiation(ctx, u, "native-original"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_multipart_initiation_dispatches SET provider_upload_id='other' WHERE multipart_upload_id=$1`, u.ID); err == nil {
		t.Fatal("observed identity reassigned")
	}
	// A replacement owner resumes the positive reply, not the create request.
	if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	u, err = restarted.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "replacement", state.ObjectMultipartInitiating, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	d, err := restarted.ReadObjectMultipartInitiation(ctx, u)
	if err != nil || !d.Dispatched || d.ProviderUploadID != "native-original" || d.DispatchToken != "original" {
		t.Fatal("restart lost original reply", d, err)
	}
	if err := restarted.ActivateObjectMultipartUpload(ctx, u.ID, u.LeaseToken, d.ProviderUploadID); err != nil {
		t.Fatal(err)
	}
}

func TestPgObjectMultipartInitiationMigrationClassifiesExistingAsUncertain(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	u, err := st.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "legacy", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261007074655000_object_multipart_initiation_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade removed live dispatch evidence")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Only this isolated fixture bypasses the busy-down block to construct the
	// previous schema with a session whose native dispatch history is unknown.
	drops := strings.SplitN(parts[1], "-- +goose StatementEnd", 2)[1]
	if _, err := tx.Exec(ctx, drops); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var dispatched bool
	var token, providerID string
	if err := tx.QueryRow(ctx, `SELECT dispatched,dispatch_token,provider_upload_id FROM object_multipart_initiation_dispatches WHERE multipart_upload_id=$1`, u.ID).Scan(&dispatched, &token, &providerID); err != nil || !dispatched || token != "" || providerID != "" {
		t.Fatal("migration classified uncertain legacy as new", dispatched, token, providerID, err)
	}
}

func TestPgObjectMultipartInitiationMigrationRoundTrip(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	raw, err := migrations.FS.ReadFile("20261007074655000_object_multipart_initiation_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	const shape = `SELECT jsonb_build_array((SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='guard_object_multipart_initiation_dispatch'),(SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='compose_object_multipart_initiation_dispatch'),(SELECT pg_get_functiondef(oid) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname='protect_object_multipart_initiation_result'),(SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_multipart_initiation_dispatch_guard'),(SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_multipart_initiation_dispatch_composition'),(SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='object_multipart_initiation_result_guard'))::text`
	var before, after string
	if err := tx.QueryRow(ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatal("round trip changed initiation guards", err)
	}
}

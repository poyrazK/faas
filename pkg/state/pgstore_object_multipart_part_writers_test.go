//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestPgObjectMultipartPartWriterGuardsRollbackRestart(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, u := activeTrackedUpload(t, st, "original-part")
	if err := st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE object_multipart_part_writers SET dispatched=true WHERE upload_id=$1 AND part_number=1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := st.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original")
	if err != nil {
		t.Fatal("rollback lost authority", err)
	}
	for _, q := range []string{
		`DELETE FROM object_multipart_part_writers WHERE upload_id=$1`,
		`UPDATE object_multipart_part_writers SET dispatched=false WHERE upload_id=$1`,
		`UPDATE object_multipart_part_writers SET transfer_token='replacement' WHERE upload_id=$1`,
		`UPDATE object_multipart_part_writers SET physical_name='other' WHERE upload_id=$1`,
		`DELETE FROM object_bucket_mutations WHERE multipart_part_writer_id IN (SELECT id FROM object_multipart_part_writers WHERE upload_id=$1)`,
		`UPDATE object_bucket_mutations SET multipart_part_writer_id=NULL WHERE multipart_part_writer_id IN (SELECT id FROM object_multipart_part_writers WHERE upload_id=$1)`,
		`DELETE FROM object_storage_multipart_part_grants WHERE upload_id=$1`,
		`UPDATE object_storage_multipart_part_grants SET transfer_token=NULL,unsafe_until=NULL WHERE upload_id=$1`,
		`UPDATE object_storage_multipart_part_grants SET unsafe_until=clock_timestamp()-interval '1 second' WHERE upload_id=$1`,
		`UPDATE object_storage_multipart_uploads SET state='aborted' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET object_key='other' WHERE id=$1`,
	} {
		_, err = pool.Exec(ctx, q, u.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatal("raw writer rewrite escaped", q, err)
		}
	}
	// Move only the isolated fixture's clock evidence past its transfer window.
	// Production attempts may not rewrite this field; the assertion above proves it.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `ALTER TABLE object_storage_multipart_part_grants DISABLE TRIGGER object_multipart_part_transfer_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET unsafe_until=clock_timestamp()-interval '1 second' WHERE upload_id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE object_storage_multipart_part_grants ENABLE TRIGGER object_multipart_part_transfer_guard`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	if _, err = restarted.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("restart replayed dispatch", err)
	}
	if err = restarted.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "replacement", 1, 30, 100, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("deadline replaced unknown writer", err)
	}
	claimed, err := restarted.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := restarted.ObjectMultipartAbortReady(ctx, u.ID, claimed.LeaseToken); err != nil || ready {
		t.Fatal("expired writer allowed abort", ready, err)
	}
	if err = restarted.FinishVerifiedObjectMultipartAbort(ctx, u.ID, claimed.LeaseToken); !errors.Is(err, state.ErrConflict) {
		t.Fatal("parent erased unknown writer", err)
	}
	if err = restarted.FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal("late proof lost original identity", err)
	}
	if ready, err := restarted.ObjectMultipartAbortReady(ctx, u.ID, claimed.LeaseToken); err != nil || !ready {
		t.Fatal("settlement retained transfer", ready, err)
	}
}

func TestPgObjectMultipartPartWriterMigrationRoundTripLegacyAndBusyDown(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	raw, err := migrations.FS.ReadFile("20261007085254000_object_multipart_part_writers.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	copyIntent, err := migrations.FS.ReadFile("20261007100924000_object_multipart_part_copy_intent.sql")
	if err != nil {
		t.Fatal(err)
	}
	copyIntentParts := strings.SplitN(string(copyIntent), "-- +goose Down", 2)
	putIntentParts := partPutMigrationParts(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, putIntentParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, copyIntentParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, copyIntentParts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, putIntentParts[0]); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	b, u := activeTrackedUpload(t, st, "legacy-part")
	// Existing transfers predate the binding. Reinstalling may preserve them as
	// uncertain evidence, but must never give them fresh dispatch ownership.
	if _, err = pool.Exec(ctx, putIntentParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, copyIntentParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if err = st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "legacy", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	// Old workers could mark the parent complete after the transfer timeout.
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='completed' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, copyIntentParts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, putIntentParts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "legacy"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("adopted uncertain legacy transfer", err)
	}
	if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "legacy"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retired uncertain legacy transfer", err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("busy downgrade deleted independent evidence")
	}
	// No existing anonymous receipt may be inferred away by the new binding.
	if _, err = st.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
		t.Fatal(err)
	}
	hold, err := st.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || hold.Requests != 2 || hold.Multipart != 0 {
		t.Fatal("legacy evidence was adopted", hold, err)
	}
}

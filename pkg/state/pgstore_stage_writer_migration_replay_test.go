//go:build !no_pg

// adr: 590 — replay must preserve original capture and writer custody.
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func replayStageWriterMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, name := range []string{
		"20261006133007028_environment_clone_configuration_guards.sql",
		"20261006161300941_object_deletion_capture_admission.sql",
		"20261007125239060_object_protection_capture_admission.sql",
		"20261007125239063_object_upload_capture_admission.sql",
		"20261007125239066_object_upload_mutation_binding.sql",
		"20261007125239070_object_multipart_mutation_binding.sql",
		"20261007125239073_object_multipart_initiation_dispatch.sql",
		"20261007125239076_object_multipart_part_writers.sql",
		"20261007125239079_object_multipart_part_copy_intent.sql",
		"20261007125239082_object_multipart_part_put_intent.sql",
		"20261007125239084_clone_configuration_parent_purge.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[0]); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			t.Fatalf("replay %s: %v", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit replay %s: %v", name, err)
		}
	}
}

func TestPgStageWriterMigrationReplayPreservesCustody(t *testing.T) {
	t.Run("configuration hold", func(t *testing.T) {
		f := newCloneConfigurationFenceFixture(t)
		if _, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err != nil {
			t.Fatal(err)
		}
		const snapshot = `SELECT jsonb_build_array(to_jsonb(g),(SELECT to_jsonb(c) FROM project_environment_clone_configuration_clock c))::text FROM project_environment_clone_configuration_guards g WHERE project_id=$1`
		var before, after string
		if err := f.pool.QueryRow(f.ctx, snapshot, f.app.ProjectID).Scan(&before); err != nil {
			t.Fatal(err)
		}
		replayStageWriterMigrations(t, f.ctx, f.pool)
		if err := f.pool.QueryRow(f.ctx, snapshot, f.app.ProjectID).Scan(&after); err != nil || before != after {
			t.Fatalf("replay changed capture authority: %v\n%s\n%s", err, before, after)
		}
		if err := f.store.UpsertAppEnvInScope(f.ctx, f.app.AccountID, f.app.ID, "production", "VERSION", "replacement"); err == nil {
			t.Fatal("replay released source configuration hold")
		}
	})
	t.Run("uncertain PUT", func(t *testing.T) {
		st, pool, ctx := pgStoreWithPool(t)
		b, u := activeTrackedUpload(t, st, "replay-put")
		if err := st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 3, 100, accountingPolicy()); err != nil {
			t.Fatal(err)
		}
		i := state.ObjectMultipartPartPutIntent{Schema: 1, DestinationKey: u.Key, ProviderUploadID: u.ProviderUploadID, ExpectedSize: 3, ExpectedSHA256: partBodySHA("abc")}
		r, err := st.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 1, "original", i)
		if err != nil {
			t.Fatal(err)
		}
		if err = st.ObserveObjectMultipartPartBody(ctx, r, i.ExpectedSHA256); err != nil {
			t.Fatal(err)
		}
		hold, err := st.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		const snapshot = `SELECT jsonb_build_array(to_jsonb(d),(SELECT to_jsonb(i) FROM object_multipart_initiation_dispatches i WHERE multipart_upload_id=d.upload_id),(SELECT to_jsonb(m) FROM object_bucket_mutations m WHERE multipart_part_writer_id=d.id))::text FROM object_multipart_part_writers d WHERE id=$1`
		var before, after string
		if err = pool.QueryRow(ctx, snapshot, r.ID).Scan(&before); err != nil {
			t.Fatal(err)
		}
		replayStageWriterMigrations(t, ctx, pool)
		if err = pool.QueryRow(ctx, snapshot, r.ID).Scan(&after); err != nil || before != after {
			t.Fatalf("replay changed original writer evidence: %v\n%s\n%s", err, before, after)
		}
		if got, err := st.ReadObjectBucketWriteFence(ctx, b, hold.Token); err != nil || got.Requests != 2 {
			t.Fatalf("replay lost custody: %+v %v", got, err)
		}
		if got, err := st.ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.BodySHA256 != i.ExpectedSHA256 {
			t.Fatalf("replay lost body identity: %+v %v", got, err)
		}
		if _, err = st.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 1, "original", i); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("replay renewed dispatch authority: %v", err)
		}
		if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("replay drained uncertain writer: %v", err)
		}
	})
}

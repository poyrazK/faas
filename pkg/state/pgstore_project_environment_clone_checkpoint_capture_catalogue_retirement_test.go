//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCheckpointCaptureRetirementAdoptedMixedAtomicCleanup(t *testing.T) {
	s, pool, l := pgCaptureRetirementFixture(t, "adopted-mixed")
	ctx := t.Context()
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("live unused target reservation accepted", err)
	}
	source, err := s.ProjectEnvironmentCloneCheckpointSourceCompensationForLease(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	var objectID, databaseID string
	for _, original := range source.Plan.Capture.Sources {
		if original.Kind == "object_storage" {
			objectID = original.ID
			if err = s.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx, l, objectID); err != nil {
				t.Fatal("unused reservation release", err)
			}
		} else {
			databaseID = original.ID
		}
	}
	manifest, err := s.ProjectEnvironmentCloneObjectManifest(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID, objectID)
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil || in.State != "planned" {
		t.Fatal("adopted reserve", err)
	}
	in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil || !dispatch {
		t.Fatal(err)
	}
	o := pgCaptureRetirementProof(t, in)
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_capture_snapshot_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN IF NEW.state='deleted' THEN RAISE EXCEPTION 'injected final catalogue failure' USING ERRCODE='40001'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_capture_snapshot_retirement BEFORE UPDATE ON project_environment_clone_postgres_snapshots FOR EACH ROW EXECUTE FUNCTION reject_capture_snapshot_retirement();`); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err == nil {
		t.Fatal("failed catalogue retirement was ignored")
	}
	var status, snapshotStatus string
	var rows int
	if err = pool.QueryRow(ctx, `SELECT c.state,s.state,(SELECT count(*) FROM project_environment_clone_checkpoint_capture_catalogues r WHERE r.operation_id=c.operation_id)
	 FROM project_environment_clone_checkpoint_capture_retirements c JOIN project_environment_clone_postgres_snapshots s ON s.operation_id=c.operation_id WHERE c.operation_id=$1`, l.Operation.ID).Scan(&status, &snapshotStatus, &rows); err != nil || status != "requested" || snapshotStatus != "retained" || rows != 0 {
		t.Fatal("terminal proof/catalogue rollback failed", status, snapshotStatus, rows, err)
	}
	if _, err = pool.Exec(ctx, "DROP TRIGGER reject_capture_snapshot_retirement ON project_environment_clone_postgres_snapshots; DROP FUNCTION reject_capture_snapshot_retirement();"); err != nil {
		t.Fatal(err)
	}
	in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil || dispatch || in.State != "requested" {
		t.Fatal("recovery redispatched", err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err != nil {
		t.Fatal("atomic native proof and catalogue retirement", err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o); err != nil {
		t.Fatal("completed replay", err)
	}
	in, err = s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, l)
	if err != nil || in.State != "retired" {
		t.Fatal(err)
	}
	replay, err := s.ProjectEnvironmentCloneCheckpointSourceCompensationForLease(ctx, l)
	if err != nil || captureRetirementDigest(t, replay) != captureRetirementDigest(t, source) {
		t.Fatal("source proof changed after catalogue cleanup", err)
	}
	snapshot, err := s.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, l, databaseID)
	if err != nil || snapshot.State != "deleted" || snapshot.ProviderSnapshotID != "original-snapshot" || !snapshot.CleanupObservedAt.Equal(o.RetiredAt) {
		t.Fatal("exact snapshot catalogue retirement", err)
	}
	after, err := s.ProjectEnvironmentCloneObjectManifest(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID, objectID)
	if err != nil || captureRetirementDigest(t, after) != captureRetirementDigest(t, manifest) {
		t.Fatal("frozen manifest evidence was lost", err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM project_environment_clone_checkpoint_capture_catalogues WHERE operation_id=$1 AND observation_sha256=$2", l.Operation.ID, in.ObservationSHA256).Scan(&rows); err != nil || rows != 2 {
		t.Fatal("incomplete catalogue proof roster", rows, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_postgres_snapshots SET state='retained',cleanup_observed_at=NULL WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("reopened catalogue passed terminal replay", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_postgres_snapshots SET state='deleted',cleanup_observed_at=$2 WHERE operation_id=$1", l.Operation.ID, o.RetiredAt); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_checkpoint_capture_catalogues SET capture_identity='foreign-manifest' WHERE operation_id=$1 AND source_kind='object_storage'", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign catalogue evidence passed terminal replay", err)
	}
}

func TestPgCheckpointCaptureRetirementAdoptedCatalogueMigration(t *testing.T) {
	s, pool, l := pgCaptureRetirementFixture(t, "adopted")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006385_clone_checkpoint_capture_catalogue_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal("empty catalogue migration round trip", err)
		}
	}
	_ = tx.Rollback(ctx)
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"planned", "requested", "retired"} {
		if status != "planned" {
			in, _, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			if status == "retired" {
				if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, pgCaptureRetirementProof(t, in)); err != nil {
					t.Fatal(err)
				}
			}
		}
		tx, err = pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, parts[1])
		_ = tx.Rollback(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_capture_catalogue_retirements_down_no_ownership" {
			t.Fatal("owned catalogue downgrade", status, err)
		}
	}
	if _, err = pool.Exec(ctx, "DELETE FROM project_environment_clone_checkpoint_capture_retirements WHERE operation_id=$1", l.Operation.ID); err == nil {
		t.Fatal("original native proof could be deleted")
	}
}

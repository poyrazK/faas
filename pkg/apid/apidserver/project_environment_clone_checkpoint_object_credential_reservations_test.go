//go:build !no_pg

// adr: 590
package apidserver

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGCloneCheckpointObjectCredentialReservationRecovery(t *testing.T) {
	f, s, d := checkpointObjectVerificationFixture(t)
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention); !errors.Is(err, state.ErrNotFound) || out.SHA256 != "" {
		t.Fatal("unverified reservation", err)
	}
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err = f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	// An insert that fails after IDs are allocated must leave no journal or IDs.
	if _, err = f.pool.Exec(t.Context(), `create function reject_checkpoint_credential_ids() returns trigger language plpgsql as $$ begin raise exception 'injected commit failure' using errcode='40001'; end $$; create trigger reject_checkpoint_credential_ids before insert on project_environment_clone_checkpoint_object_credential_ids for each row execute function reject_checkpoint_credential_ids()`); err != nil {
		t.Fatal(err)
	}
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention); err == nil || out.SHA256 != "" {
		t.Fatal("failed insert returned identities", err)
	}
	var count int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_object_credential_ids").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed reservation persisted", count, err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_checkpoint_credential_ids on project_environment_clone_checkpoint_object_credential_ids; drop function reject_checkpoint_credential_ids()"); err != nil {
		t.Fatal(err)
	}
	// A worker expiry after insertion must be caught by the final lease fence.
	if _, err = f.pool.Exec(t.Context(), `create function expire_checkpoint_credential_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_checkpoint_credential_worker after insert on project_environment_clone_checkpoint_object_credential_ids for each row execute function expire_checkpoint_credential_worker()`); err != nil {
		t.Fatal(err)
	}
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("late worker expiry returned identities", err)
	}
	if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_object_credential_ids").Scan(&count); err != nil || count != 0 {
		t.Fatal("late worker fence failed to roll back identities", count, err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger expire_checkpoint_credential_worker on project_environment_clone_checkpoint_object_credential_ids; drop function expire_checkpoint_credential_worker()"); err != nil {
		t.Fatal(err)
	}
	// Discard the first response to simulate a committed reservation's lost reply.
	if _, err = s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention); err != nil {
		t.Fatal(err)
	}
	original, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention)
	if err != nil || len(original.Members) == 0 {
		t.Fatal("reservation", err)
	}
	again, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention)
	if err != nil || !reflect.DeepEqual(original, again) {
		t.Fatal("retry reallocated identities", err)
	}
	old := f.lease
	reclaimCommonCheckpointWorker(t, f)
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), old, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("stale worker returned identities", err)
	}
	f.lease, _, err = f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err = f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	again, err = s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention)
	if err != nil || !reflect.DeepEqual(original, again) {
		t.Fatal("handoff reallocated identities", err)
	}
	expired := retention
	expired.RetainedUntil = time.Now()
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, expired); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("expired retention replay", err)
	}
	target := original.Inputs.Buckets[0].Target.TargetBucketID
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set public_read=true where id=$1", target); err != nil {
		t.Fatal(err)
	}
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("public target replay returned identities", err)
	}
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set public_read=false where id=$1", target); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../migrations/20261011232006353_clone_checkpoint_object_credential_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_credential_ids_down_no_ownership" {
		t.Fatal("owned reservation downgrade", err)
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_credential_ids set plan_sha256=repeat('a',64) where operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("tampered journal returned identities", err)
	}
}

func TestPGCloneCheckpointObjectCredentialReservationMigration(t *testing.T) {
	f, _, _ := checkpointObjectVerificationFixture(t)
	migration, err := os.ReadFile("../../../migrations/20261011232006353_clone_checkpoint_object_credential_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	publicationMigration, err := os.ReadFile("../../../migrations/20261011232006359_clone_checkpoint_object_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.Split(string(publicationMigration), "-- +goose Down")[1]); err != nil {
		t.Fatal("dependent publication downgrade", err)
	}
	valuesMigration, err := os.ReadFile("../../../migrations/20261011232006356_clone_checkpoint_object_credential_values.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.Split(string(valuesMigration), "-- +goose Down")[1]); err != nil {
		t.Fatal("dependent empty downgrade", err)
	}
	if _, err = tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("empty downgrade", err)
	}
	up := strings.Split(parts[0], "-- +goose Up")[1]
	if _, err = tx.Exec(t.Context(), up); err != nil {
		t.Fatal("upgrade", err)
	}
	if _, err = tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("empty round trip", err)
	}
}

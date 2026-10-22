//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointUnusedReservationLostReplyStore struct {
	*checkpointTargetLostReplyStore
	lose bool
}

func (s *checkpointUnusedReservationLostReplyStore) ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) error {
	err := s.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx, l, id)
	if err == nil && s.lose {
		s.lose = false
		return objectstorage.ErrUnavailable
	}
	return err
}

func TestPGCloneCheckpointUnusedObjectReservationRecovery(t *testing.T) {
	f, store, _ := checkpointTargetFixture(t)
	compensateCheckpointObjectFixture(t, f)
	var sourceID string
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			sourceID = r.SourceID
			break
		}
	}
	admission := f.srv.cloneWorkerAdmission
	f.srv.cloneWorkerAdmission = func(context.Context) error { return state.ErrConflict }
	if err := f.srv.retireCheckpointObjectSource(t.Context(), f.lease, nil, statetest.NewPg(store), sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("local cleanup ignored admission", err)
	}
	f.srv.cloneWorkerAdmission = admission
	wrapper := &checkpointUnusedReservationLostReplyStore{checkpointTargetLostReplyStore: store, lose: true}
	f.srv.store = statetest.NewPg(wrapper)
	d := &checkpointObjectRetirementTestDriver{t: t, f: f, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{}}
	var err error
	f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("lost completion reply", err)
	}
	old := f.lease
	reclaimCommonCheckpointWorker(t, f)
	var id string
	if err = f.pool.QueryRow(t.Context(), "select source_bucket_id::text from project_environment_clone_checkpoint_unused_object_reservations where operation_id=$1", f.lease.Operation.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), old, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale replay", err)
	}
	for i := 0; i < 2; i++ {
		f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
		if err != nil {
			t.Fatal("handoff/replay", err)
		}
	}
	var deleted, completed int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from object_buckets where environment_clone_operation_id=$1 and state='deleted'", f.lease.Operation.ID).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_unused_object_reservations where operation_id=$1", f.lease.Operation.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if deleted != 2 || completed != 2 || d.calls != 0 {
		t.Fatal("local cleanup dispatched native IO", deleted, completed, d.calls)
	}
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set state='provisioning' where environment_clone_operation_id=$1 and environment_clone_source_bucket_id=$2", f.lease.Operation.ID, id); err != nil {
		t.Fatal(err)
	}
	if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revival accepted", err)
	}
}

func TestPGCloneCheckpointUnusedObjectReservationFences(t *testing.T) {
	for _, failure := range []string{"capturing", "planned-native", "foreign-placement", "public", "ready", "bucket-lease", "unproved-deletion", "missing-release", "corrupt-manifest", "foreign-revoked-credential", "expired-worker", "late-journal-expiry", "late-bucket-expiry"} {
		t.Run(failure, func(t *testing.T) {
			f, store, _ := checkpointTargetFixture(t)
			var id, targetID, appID string
			for _, r := range f.lease.Operation.Resources {
				if r.Kind == "object_storage" {
					id = r.SourceID
					targetID = r.TargetID
					break
				}
			}
			if err := f.pool.QueryRow(t.Context(), "select app_id::text from object_buckets where id=$1", targetID).Scan(&appID); err != nil {
				t.Fatal(err)
			}
			if failure == "planned-native" {
				if _, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectTarget(t.Context(), f.lease, appID, id, targetID); err != nil {
					t.Fatal(err)
				}
			}
			if failure != "capturing" {
				compensateCheckpointObjectFixture(t, f)
			}
			var sql string
			switch failure {
			case "foreign-placement":
				sql = "update object_buckets set region='foreign' where id=$1"
			case "public":
				sql = "update object_buckets set public_read=true where id=$1"
			case "ready":
				sql = "update object_buckets set state='ready' where id=$1"
			case "bucket-lease":
				sql = "update object_buckets set lease_token=gen_random_uuid(),lease_until=clock_timestamp()+interval '1 minute' where id=$1"
			case "unproved-deletion":
				sql = "update object_buckets set state='deleted' where id=$1"
			}
			if sql != "" {
				if _, err := f.pool.Exec(t.Context(), sql, targetID); err != nil {
					t.Fatal(err)
				}
			}
			switch failure {
			case "missing-release":
				_, err := f.pool.Exec(t.Context(), "delete from project_environment_clone_checkpoint_releases where operation_id=$1", f.lease.Operation.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "corrupt-manifest":
				_, err := f.pool.Exec(t.Context(), "update project_environment_clone_object_manifests set manifest_hash=$2 where operation_id=$1 and source_bucket_id=$3", f.lease.Operation.ID, strings.Repeat("f", 64), id)
				if err != nil {
					t.Fatal(err)
				}
			case "foreign-revoked-credential":
				access, _, err := api.GenerateObjectS3Credential()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.pool.Exec(t.Context(), "insert into object_storage_s3_credentials(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,revoked_at) values($1,$2,$3,$4,$5,'kid','foreign','read','revoked',clock_timestamp())", uuid.NewString(), f.lease.Operation.AccountID, targetID, access, []byte("opaque")); err != nil {
					t.Fatal(err)
				}
			case "expired-worker":
				_, err := f.pool.Exec(t.Context(), "update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=$1", f.lease.Operation.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "late-journal-expiry":
				_, err := f.pool.Exec(t.Context(), `create function expire_unused_journal_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_unused_journal_worker after insert on project_environment_clone_checkpoint_unused_object_reservations for each row execute function expire_unused_journal_worker()`)
				if err != nil {
					t.Fatal(err)
				}
			case "late-bucket-expiry":
				_, err := f.pool.Exec(t.Context(), `create function expire_unused_bucket_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.environment_clone_operation_id; return NEW; end $$; create trigger expire_unused_bucket_worker after update on object_buckets for each row when(NEW.state='deleted') execute function expire_unused_bucket_worker()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unsafe unused cleanup", err)
			}
			var completed int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_unused_object_reservations where operation_id=$1", f.lease.Operation.ID).Scan(&completed); err != nil || completed != 0 {
				t.Fatal("partial completion committed", completed, err)
			}
			if failure == "late-journal-expiry" || failure == "late-bucket-expiry" {
				var state string
				if err := f.pool.QueryRow(t.Context(), "select state from object_buckets where id=$1", targetID).Scan(&state); err != nil || state != "provisioning" {
					t.Fatal("late fence did not roll back bucket", state, err)
				}
			}
		})
	}
}

func TestPGCloneCheckpointUnusedObjectReservationMigration(t *testing.T) {
	f, store, _ := checkpointTargetFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006371_clone_checkpoint_unused_object_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("empty unused reservation migration", err)
		}
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	compensateCheckpointObjectFixture(t, f)
	var id string
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			id = r.SourceID
			break
		}
	}
	if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_unused_reservations_down_no_ownership" {
		t.Fatal("owned local evidence downgrade", err)
	}
}

//go:build !no_pg

// adr: 590
package apidserver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointObjectWithdrawalLostReplyStore struct {
	*checkpointObjectVerificationTestStore
	lose bool
}

func (s *checkpointObjectWithdrawalLostReplyStore) WithdrawProjectEnvironmentCloneCheckpointObjects(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	err := s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(ctx, l)
	if err == nil && s.lose {
		s.lose = false
		return objectstorage.ErrUnavailable
	}
	return err
}
func compensateCheckpointObjectFixture(t *testing.T, f *cloneCoordinatorFixture) {
	t.Helper()
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = f.store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
}
func checkpointObjectWithdrawalFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointObjectVerificationTestStore, state.ProjectEnvironmentCloneCheckpointObjectPublicationPlan) {
	t.Helper()
	f, store, d, plan, _ := checkpointObjectPublicationFixture(t)
	var err error
	f.lease, _, err = f.srv.publishCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	compensateCheckpointObjectFixture(t, f)
	return f, store, plan
}
func assertCheckpointObjectWithdrawalState(t *testing.T, f *cloneCoordinatorFixture, plan state.ProjectEnvironmentCloneCheckpointObjectPublicationPlan, withdrawn bool) {
	t.Helper()
	var journal string
	if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_object_publications where operation_id=$1", plan.OperationID).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	want := "published"
	if withdrawn {
		want = "withdrawn"
	}
	if journal != want {
		t.Fatal("publication state", journal)
	}
	for _, b := range plan.Buckets {
		for _, p := range b.Preparations {
			var status string
			var stampMatches bool
			if err := f.pool.QueryRow(t.Context(), "select c.status,c.revoked_at is not distinct from j.withdrawn_at from object_storage_s3_credentials c join project_environment_clone_checkpoint_object_publications j on j.operation_id=$2 where c.id=$1", p.Credential.ID, plan.OperationID).Scan(&status, &stampMatches); err != nil {
				t.Fatal(err)
			}
			expected := "active"
			if withdrawn {
				expected = "revoked"
			}
			if status != expected || !stampMatches {
				t.Fatal("credential retirement", status, stampMatches)
			}
			var n int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where managed_object_storage_credential_id=$1", p.Credential.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			expectedCount := len(p.Secrets)
			if withdrawn {
				expectedCount = 0
			}
			if n != expectedCount {
				t.Fatal("owned secrets", n)
			}
		}
		var n int
		if err := f.pool.QueryRow(t.Context(), "select count(*) from object_storage_access_grants where bucket_id=$1", b.TargetBucketID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		expected := len(b.AccessGrants)
		if withdrawn {
			expected = 0
		}
		if n != expected {
			t.Fatal("owned grants", n)
		}
	}
}
func TestPGCloneCheckpointObjectWithdrawalRecovery(t *testing.T) {
	f, base, plan := checkpointObjectWithdrawalFixture(t)
	lost := &checkpointObjectWithdrawalLostReplyStore{checkpointObjectVerificationTestStore: base, lose: true}
	f.srv.store = statetest.NewPg(lost)
	app := plan.Buckets[0].AppID
	if _, err := f.pool.Exec(t.Context(), "insert into app_secrets(account_id,app_id,scope,key,ciphertext) values($1,$2,$3,'UNRELATED',$4)", plan.AccountID, app, plan.TargetEnvironment, []byte("unrelated")); err != nil {
		t.Fatal(err)
	}
	var err error
	f.lease, err = f.srv.withdrawCheckpointObjects(t.Context(), f.lease)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("lost withdrawal reply", err)
	}
	old := f.lease
	assertCheckpointObjectWithdrawalState(t, f, plan, true)
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.withdrawCheckpointObjects(t.Context(), f.lease)
	if err != nil {
		t.Fatal("withdrawal handoff", err)
	}
	if err = base.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), old); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale withdrawal", err)
	}
	var ciphertext []byte
	if err = f.pool.QueryRow(t.Context(), "select ciphertext from app_secrets where app_id=$1 and scope=$2 and key='UNRELATED'", app, plan.TargetEnvironment).Scan(&ciphertext); err != nil || !bytes.Equal(ciphertext, []byte("unrelated")) {
		t.Fatal("unrelated secret changed", err)
	}
	var sourceGrants int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from object_storage_access_grants g join object_buckets b on b.id=g.bucket_id where b.account_id=$1 and b.scope='production'", plan.AccountID).Scan(&sourceGrants); err != nil || sourceGrants != 2 {
		t.Fatal("source grants changed", sourceGrants, err)
	}
	// Completed native cleanup can change bucket state without recreating metadata.
	for _, b := range plan.Buckets {
		if _, err = f.pool.Exec(t.Context(), "update object_buckets set state='deleted' where id=$1", b.TargetBucketID); err != nil {
			t.Fatal(err)
		}
	}
	if err = base.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal("replay after target deletion", err)
	}
	for _, b := range plan.Buckets {
		for _, p := range b.Preparations {
			if _, _, err = base.PgStore.ResolveObjectS3Credential(t.Context(), p.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("retired credential resolved", err)
			}
		}
	}
	if out, err := base.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease, plan.SHA256, state.ProjectEnvironmentCloneCheckpointReleaseObservation{}); !errors.Is(err, state.ErrConflict) || out.State != "" {
		t.Fatal("withdrawn publication revived", err)
	}
}
func TestPGCloneCheckpointObjectWithdrawalOwnership(t *testing.T) {
	for _, tamper := range []string{"credential", "extra revoked credential", "grant", "secret owner", "missing secret", "journal", "target name"} {
		t.Run(tamper, func(t *testing.T) {
			f, store, plan := checkpointObjectWithdrawalFixture(t)
			var managed state.ProjectEnvironmentCloneObjectCredentialPreparation
			var bucket state.ProjectEnvironmentCloneCheckpointObjectPublicationBucket
			for _, b := range plan.Buckets {
				for _, p := range b.Preparations {
					if len(p.Secrets) > 0 {
						managed = p
						bucket = b
					}
				}
			}
			var err error
			switch tamper {
			case "credential":
				_, err = f.pool.Exec(t.Context(), "update object_storage_s3_credentials set label='changed' where id=$1", managed.Credential.ID)
			case "extra revoked credential":
				access, _, keyErr := api.GenerateObjectS3Credential()
				if keyErr != nil {
					t.Fatal(keyErr)
				}
				_, err = f.pool.Exec(t.Context(), "insert into object_storage_s3_credentials(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,revoked_at) values($1,$2,$3,$4,$5,'kid','foreign','read','revoked',now())", uuid.NewString(), plan.AccountID, bucket.TargetBucketID, access, []byte("foreign"))
			case "grant":
				_, err = f.pool.Exec(t.Context(), "update object_storage_access_grants set permission='write' where bucket_id=$1", bucket.TargetBucketID)
			case "secret owner":
				other := f.apps[0].ID
				if other == managed.AppID {
					other = f.apps[1].ID
				}
				_, err = f.pool.Exec(t.Context(), "update app_secrets set app_id=$2 where managed_object_storage_credential_id=$1", managed.Credential.ID, other)
			case "missing secret":
				_, err = f.pool.Exec(t.Context(), "delete from app_secrets where managed_object_storage_credential_id=$1 and key=$2", managed.Credential.ID, managed.Secrets[0].Key)
			case "journal":
				_, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_publications set plan_sha256=repeat('a',64) where operation_id=$1", plan.OperationID)
			case "target name":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set name='changed' where id=$1", bucket.TargetBucketID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("foreign matrix withdrawn", err)
			}
			var retired int
			if err = f.pool.QueryRow(t.Context(), "select count(*) from object_storage_s3_credentials where id=$1 and status='revoked'", managed.Credential.ID).Scan(&retired); err != nil || retired != 0 {
				t.Fatal("partial withdrawal", retired, err)
			}
			var journal string
			if err = f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_object_publications where operation_id=$1", plan.OperationID).Scan(&journal); err != nil || journal != "published" {
				t.Fatal("journal changed", err)
			}
		})
	}
}
func TestPGCloneCheckpointObjectWithdrawalRollbackAndFences(t *testing.T) {
	f, store, plan := checkpointObjectWithdrawalFixture(t)
	if _, err := f.pool.Exec(t.Context(), `create function expire_object_withdrawal_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_object_withdrawal_worker after update on project_environment_clone_checkpoint_object_publications for each row when(NEW.state='withdrawn') execute function expire_object_withdrawal_worker()`); err != nil {
		t.Fatal(err)
	}
	if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late withdrawal expiry", err)
	}
	assertCheckpointObjectWithdrawalState(t, f, plan, false)
	if _, err := f.pool.Exec(t.Context(), "drop trigger expire_object_withdrawal_worker on project_environment_clone_checkpoint_object_publications;drop function expire_object_withdrawal_worker()"); err != nil {
		t.Fatal(err)
	}
	if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal("withdrawal retry", err)
	}
	assertCheckpointObjectWithdrawalState(t, f, plan, true)
	for _, b := range plan.Buckets {
		for _, p := range b.Preparations {
			if _, err := f.pool.Exec(t.Context(), "update object_storage_s3_credentials set status='active',revoked_at=null where id=$1", p.Credential.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("revived row accepted", err)
			}
			return
		}
	}
	t.Fatal("missing credentials")
}
func TestPGCloneCheckpointObjectWithdrawalAbsentAndStateFence(t *testing.T) {
	f, store, _, _, _ := checkpointObjectPublicationFixture(t)
	if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("capture allowed withdrawal", err)
	}
	compensateCheckpointObjectFixture(t, f)
	if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal("absent publication", err)
	}
	assertCheckpointObjectPublicationAbsent(t, f)
}
func TestPGCloneCheckpointObjectWithdrawalMigration(t *testing.T) {
	f, store, plan := checkpointObjectWithdrawalFixture(t)
	raw, err := os.ReadFile("../../../migrations/20261011232006361_clone_checkpoint_object_withdrawals.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], strings.Split(parts[0], "-- +goose Up")[1], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("published withdrawal migration round trip", err)
		}
	}
	_ = tx.Rollback(t.Context())
	if err = store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	assertCheckpointObjectWithdrawalState(t, f, plan, true)
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_withdrawals_down_no_retirement" {
		t.Fatal("retirement downgrade", err)
	}
}

//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointObjectPublicationLostReplyStore struct {
	*checkpointObjectVerificationTestStore
	lose bool
}

func (s *checkpointObjectPublicationLostReplyStore) PublishProjectEnvironmentCloneCheckpointObjects(ctx context.Context, l state.ProjectEnvironmentCloneLease, digest string, retention state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectPublication, error) {
	out, err := s.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(ctx, l, digest, retention)
	if err == nil && s.lose {
		s.lose = false
		return state.ProjectEnvironmentCloneCheckpointObjectPublication{}, objectstorage.ErrUnavailable
	}
	return out, err
}

func checkpointObjectPublicationFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointObjectVerificationTestStore, *checkpointObjectVerificationTestDriver, state.ProjectEnvironmentCloneCheckpointObjectPublicationPlan, string) {
	t.Helper()
	var keyID string
	f, store, d := checkpointObjectVerificationFixture(t, func(f *cloneCoordinatorFixture, second state.ObjectBucket) {
		_, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.store.CreateAPIKey(t.Context(), second.AccountID, hash, "checkpoint-reader", []string{"storage:read", "storage:write"})
		if err != nil {
			t.Fatal(err)
		}
		keyID = key.ID
		if _, err = f.store.SetObjectBucketAccessGrant(t.Context(), second.AccountID, second.ID, key.ID, "read"); err != nil {
			t.Fatal(err)
		}
		var first string
		if err = f.pool.QueryRow(t.Context(), "select id::text from object_buckets where account_id=$1 and app_id=$2 and scope='production'", second.AccountID, f.apps[0].ID).Scan(&first); err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.SetObjectBucketAccessGrant(t.Context(), second.AccountID, first, key.ID, "read_write"); err != nil {
			t.Fatal(err)
		}
		access, _, err := api.GenerateObjectS3Credential()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: second.AccountID, BucketID: first, AccessKeyID: access, SecretSealed: []byte("opaque-original"), KID: "original", Label: "standalone", Permission: "read", Status: "active"}, api.MaxObjectS3CredentialsPerBucket); err != nil {
			t.Fatal(err)
		}
	})
	checkpointObjectValueIdentity(t)
	var err error
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, _, err = f.srv.prepareCheckpointObjectCredentialValues(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectPublicationPlan(t.Context(), f.lease, retention)
	if err != nil {
		t.Fatal(err)
	}
	return f, store, d, plan, keyID
}
func assertCheckpointObjectPublicationAbsent(t *testing.T, f *cloneCoordinatorFixture) {
	t.Helper()
	for _, query := range []string{
		"select count(*) from project_environment_clone_checkpoint_object_publications where operation_id=$1",
		"select count(*) from object_storage_s3_credentials where bucket_id in(select target_bucket_id from project_environment_clone_checkpoint_object_targets where operation_id=$1)",
		"select count(*) from object_storage_access_grants where bucket_id in(select target_bucket_id from project_environment_clone_checkpoint_object_targets where operation_id=$1)",
	} {
		var n int
		if err := f.pool.QueryRow(t.Context(), query, f.lease.Operation.ID).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial publication", n, err)
		}
	}
}
func TestPGCloneCheckpointObjectPublicationRecovery(t *testing.T) {
	f, base, d, plan, keyID := checkpointObjectPublicationFixture(t)
	lost := &checkpointObjectPublicationLostReplyStore{checkpointObjectVerificationTestStore: base, lose: true}
	f.srv.store = statetest.NewPg(lost)
	var err error
	f.lease, _, err = f.srv.publishCheckpointObjects(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("lost publication reply", err)
	}
	originalLease := f.lease
	reclaimCommonCheckpointWorker(t, f)
	var out state.ProjectEnvironmentCloneCheckpointObjectPublication
	f.lease, out, err = f.srv.publishCheckpointObjects(t.Context(), f.lease, d)
	if err != nil || out.State != "published" || !reflect.DeepEqual(plan, out.Plan) {
		t.Fatal("owned handoff replay", err)
	}
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if failed, err := base.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), originalLease, plan.SHA256, retention); !errors.Is(err, state.ErrConflict) || failed.State != "" {
		t.Fatal("stale worker publication", err)
	}
	expired := retention
	expired.RetainedUntil = time.Now()
	if failed, err := base.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease, plan.SHA256, expired); !errors.Is(err, state.ErrConflict) || failed.State != "" {
		t.Fatal("expired retention replay", err)
	}
	grantOnly := false
	for _, b := range plan.Buckets {
		if len(b.Preparations) == 0 {
			grantOnly = true
		}
		for _, p := range b.Preparations {
			if _, _, err := base.PgStore.ResolveObjectS3Credential(t.Context(), p.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("private credential usable", err)
			}
		}
		if allowed, err := base.PgStore.ObjectBucketKeyCan(t.Context(), plan.AccountID, b.TargetBucketID, keyID, "read"); err != nil || allowed {
			t.Fatal("private API-key grant usable", err)
		}
	}
	if !grantOnly {
		t.Fatal("missing grant-only coverage")
	}
	b := plan.Buckets[0]
	if _, err = f.pool.Exec(t.Context(), "update object_storage_access_grants set permission='write' where bucket_id=$1", b.TargetBucketID); err != nil {
		t.Fatal(err)
	}
	if failed, err := base.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease, plan.SHA256, retention); !errors.Is(err, state.ErrConflict) || failed.State != "" {
		t.Fatal("changed grant owned", err)
	}
	if _, err = f.pool.Exec(t.Context(), "update object_storage_access_grants set permission=$2 where bucket_id=$1", b.TargetBucketID, b.AccessGrants[0].Permission); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range plan.Buckets {
		for _, p := range bucket.Preparations {
			if len(p.Secrets) > 0 {
				if _, err = f.pool.Exec(t.Context(), "update app_secrets set ciphertext=$2 where managed_object_storage_credential_id=$1", p.Credential.ID, []byte("tampered")); err != nil {
					t.Fatal(err)
				}
				if failed, err := base.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease, plan.SHA256, retention); !errors.Is(err, state.ErrConflict) || failed.State != "" {
					t.Fatal("changed secret owned", err)
				}
				return
			}
		}
	}
	t.Fatal("missing managed binding coverage")
}

func TestPGCloneCheckpointObjectPublicationRollback(t *testing.T) {
	for _, failure := range []string{"revoked key", "missing scope", "admin key", "secret collision", "secret quota", "late expiry", "wrong plan"} {
		t.Run(failure, func(t *testing.T) {
			f, store, d, plan, keyID := checkpointObjectPublicationFixture(t)
			retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			digest := plan.SHA256
			switch failure {
			case "revoked key":
				_, err = f.pool.Exec(t.Context(), "update api_keys set status='revoked' where id=$1", keyID)
			case "missing scope":
				_, err = f.pool.Exec(t.Context(), "update api_keys set scopes=ARRAY['storage:read']::text[] where id=$1", keyID)
			case "admin key":
				_, err = f.pool.Exec(t.Context(), "update api_keys set scopes=ARRAY['admin','storage:read','storage:write']::text[] where id=$1", keyID)
			case "secret collision":
				for _, b := range plan.Buckets {
					for _, p := range b.Preparations {
						if len(p.Secrets) > 0 {
							s := p.Secrets[0]
							_, err = f.pool.Exec(t.Context(), "insert into app_secrets(account_id,app_id,scope,key,ciphertext) values($1,$2,$3,$4,$5)", s.AccountID, s.AppID, s.Scope, s.Key, []byte("unrelated"))
						}
					}
				}
			case "secret quota":
				max := api.MustLimitsFor(api.PlanFree).SecretCountMax
				_, err = f.pool.Exec(t.Context(), "update accounts set plan='free' where id=$1", plan.AccountID)
				if err == nil {
					for _, b := range plan.Buckets {
						if len(b.Preparations) > 0 {
							_, err = f.pool.Exec(t.Context(), "insert into app_secrets(account_id,app_id,scope,key,ciphertext) select $1,$2,$3,'QUOTA_'||n,$4 from generate_series(1,$5::int) n", plan.AccountID, b.AppID, plan.TargetEnvironment, []byte("unrelated"), max)
						}
					}
				}
			case "late expiry":
				_, err = f.pool.Exec(t.Context(), `create function expire_object_publication_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_object_publication_worker after insert on project_environment_clone_checkpoint_object_publications for each row execute function expire_object_publication_worker()`)
			case "wrong plan":
				digest = strings.Repeat("a", 64)
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := store.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease, digest, retention)
			if err == nil || out.State != "" || out.Plan.SHA256 != "" {
				t.Fatal("failed publication returned material", err)
			}
			if failure == "secret quota" && !errors.Is(err, state.ErrQuotaExceeded) {
				t.Fatal("quota error", err)
			}
			assertCheckpointObjectPublicationAbsent(t, f)
			var ownedSecrets int
			if err = f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where scope=$1 and managed_object_storage_credential_id is not null", plan.TargetEnvironment).Scan(&ownedSecrets); err != nil || ownedSecrets != 0 {
				t.Fatal("partial managed secrets", ownedSecrets, err)
			}
		})
	}
}
func TestPGCloneCheckpointObjectPublicationMigration(t *testing.T) {
	f, store, d, plan, _ := checkpointObjectPublicationFixture(t)
	raw, err := os.ReadFile("../../../migrations/20261011232006359_clone_checkpoint_object_publications.sql")
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
			t.Fatal("empty publication round trip", err)
		}
	}
	_ = tx.Rollback(t.Context())
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease, plan.SHA256, retention); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_publications_down_no_ownership" {
		t.Fatal("owned publication downgrade", err)
	}
}

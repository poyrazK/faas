//go:build !no_pg

// adr: 590
package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointObjectValueLostReplyStore struct {
	*checkpointObjectVerificationTestStore
	lose bool
}

func (s *checkpointObjectValueLostReplyStore) PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(ctx context.Context, l state.ProjectEnvironmentCloneLease, in state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, retention state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	out, err := s.checkpointObjectVerificationTestStore.PgStore.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(ctx, l, in, retention)
	if err == nil && s.lose {
		s.lose = false
		return state.ProjectEnvironmentCloneCheckpointObjectCredentialValues{}, objectstorage.ErrUnavailable
	}
	return out, err
}
func checkpointObjectValueIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient, oldIdentities, oldHMAC := setSecretRecipient, mfaIdentities, hostHMACKey
	t.Cleanup(func() { setSecretRecipient, mfaIdentities, hostHMACKey = oldRecipient, oldIdentities, oldHMAC })
	setSecretRecipient = func() *age.X25519Recipient { return id.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{id} }
	hostHMACKey = func() []byte { return []byte("object-checkpoint-test-hmac") }
	return id
}
func TestPGCloneCheckpointObjectCredentialValuesRecovery(t *testing.T) {
	f, base, d := checkpointObjectVerificationFixture(t)
	identity := checkpointObjectValueIdentity(t)
	store := &checkpointObjectValueLostReplyStore{checkpointObjectVerificationTestStore: base, lose: true}
	f.srv.store = statetest.NewPg(store)
	var err error
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, _, err = f.srv.prepareCheckpointObjectCredentialValues(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("lost commit reply", err)
	}
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), f.lease, retention)
	if err != nil || len(original.Preparations) == 0 {
		t.Fatal("sealed matrix", err)
	}
	plan, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectPublicationPlan(t.Context(), f.lease, retention)
	if err != nil || plan.ValuesSHA256 != original.SHA256 || len(plan.Buckets) == 0 {
		t.Fatal("authenticated publication plan", err)
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range original.Preparations {
		ns, key, err := secretbox.OpenBytes(identity, p.Credential.SecretSealed)
		if err != nil || ns != s3gateway.CredentialSecretNamespace || len(key) == 0 {
			t.Fatal("credential envelope", err)
		}
		if bytes.Contains(encoded, key) {
			t.Fatal("plaintext signing key in journal")
		}
		for _, s := range p.Secrets {
			env, err := secretbox.Open(identity, s.Ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case strings.HasSuffix(s.Key, "_SECRET_ACCESS_KEY"):
				if env[s.Key] != string(key) {
					t.Fatal("binding signing key mismatch")
				}
			case strings.HasSuffix(s.Key, "_ACCESS_KEY_ID"):
				if env[s.Key] != p.Credential.AccessKeyID {
					t.Fatal("binding access ID mismatch")
				}
			case strings.HasSuffix(s.Key, "_ENDPOINT"):
				if env[s.Key] != original.Endpoint {
					t.Fatal("binding endpoint mismatch")
				}
			case strings.HasSuffix(s.Key, "_REGION"):
				if env[s.Key] != original.Region {
					t.Fatal("binding region mismatch")
				}
			case strings.HasSuffix(s.Key, "_ADDRESSING_STYLE"):
				if env[s.Key] != "path" {
					t.Fatal("binding addressing mismatch")
				}
			case strings.HasSuffix(s.Key, "_BUCKET"):
				if env[s.Key] != state.ProjectEnvironmentCloneObjectBucketName(f.lease.Operation, p.AppID, p.SourceBucketID) {
					t.Fatal("binding target bucket mismatch")
				}
			}
		}
		clear(key)
	}
	var rows int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from object_storage_s3_credentials where bucket_id in (select target_bucket_id from project_environment_clone_checkpoint_object_targets where operation_id=$1)", f.lease.Operation.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("credentials published during sealing", rows, err)
	}
	if err = f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where scope='stage'").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("secrets published during sealing", rows, err)
	}
	r, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention)
	if err != nil {
		t.Fatal(err)
	}
	alternative, err := f.srv.newCheckpointObjectCredentialValues(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	winner, err := store.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), f.lease, alternative, retention)
	if err != nil || !reflect.DeepEqual(original, winner) {
		t.Fatal("concurrent proposal replaced signing material", err)
	}
	old := f.lease
	reclaimCommonCheckpointWorker(t, f)
	if out, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), old, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("stale worker read sealed material", err)
	}
	if out, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectPublicationPlan(t.Context(), old, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("stale worker read publication plan", err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return nil }
	mfaIdentities = func() []*age.X25519Identity { return nil }
	hostHMACKey = func() []byte { return nil }
	var recovered state.ProjectEnvironmentCloneCheckpointObjectCredentialValues
	f.lease, recovered, err = f.srv.prepareCheckpointObjectCredentialValues(t.Context(), f.lease, d)
	if err != nil || !reflect.DeepEqual(original, recovered) {
		t.Fatal("handoff regenerated or opened keys", err)
	}
	retention, err = f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectPublicationPlan(t.Context(), f.lease, retention); err != nil || !reflect.DeepEqual(plan, replay) {
		t.Fatal("handoff changed publication plan", err)
	}
	target := r.Inputs.Buckets[0].Target.TargetBucketID
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set name='changed' where id=$1", target); err != nil {
		t.Fatal(err)
	}
	if out, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("renamed target returned sealed values", err)
	}
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set name=$2 where id=$1", target, state.ProjectEnvironmentCloneObjectBucketName(f.lease.Operation, r.Inputs.Buckets[0].AppID, r.Inputs.Buckets[0].Source.ID)); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../migrations/20261011232006356_clone_checkpoint_object_credential_values.sql")
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
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_values_down_no_ownership" {
		t.Fatal("owned sealed values downgrade", err)
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_values set payload_sha256=repeat('a',64) where operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("tampered journal returned keys", err)
	}
	if out, err := store.PgStore.ProjectEnvironmentCloneCheckpointObjectPublicationPlan(t.Context(), f.lease, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("tampered journal returned publication plan", err)
	}
}

func TestPGCloneCheckpointObjectCredentialValuesRollbackAndMigration(t *testing.T) {
	f, store, d := checkpointObjectVerificationFixture(t)
	checkpointObjectValueIdentity(t)
	var err error
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(t.Context(), f.lease, retention)
	if err != nil {
		t.Fatal(err)
	}
	in, err := f.srv.newCheckpointObjectCredentialValues(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `create function expire_object_value_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_object_value_worker after insert on project_environment_clone_checkpoint_object_values for each row execute function expire_object_value_worker()`); err != nil {
		t.Fatal(err)
	}
	if out, err := store.PgStore.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), f.lease, in, retention); !errors.Is(err, state.ErrConflict) || out.SHA256 != "" {
		t.Fatal("late worker expiry returned values", err)
	}
	var count int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_object_values").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed transaction persisted signing keys", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger expire_object_value_worker on project_environment_clone_checkpoint_object_values; drop function expire_object_value_worker()"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../migrations/20261011232006356_clone_checkpoint_object_credential_values.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	publicationMigration, err := os.ReadFile("../../../migrations/20261011232006359_clone_checkpoint_object_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.Split(string(publicationMigration), "-- +goose Down")[1]); err != nil {
		t.Fatal("dependent publication downgrade", err)
	}
	if _, err = tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("empty downgrade", err)
	}
	if _, err = tx.Exec(t.Context(), strings.Split(parts[0], "-- +goose Up")[1]); err != nil {
		t.Fatal("upgrade", err)
	}
	_ = tx.Rollback(t.Context())
	if _, err = store.PgStore.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(t.Context(), f.lease, in, retention); err != nil {
		t.Fatal("rollback recovery", err)
	}
}

func TestCheckpointObjectCredentialValuesEmptyWithoutKeys(t *testing.T) {
	old := setSecretRecipient
	t.Cleanup(func() { setSecretRecipient = old })
	setSecretRecipient = func() *age.X25519Recipient { t.Fatal("empty matrix requested encryption keys"); return nil }
	s := &server{}
	r := state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation{Inputs: state.ProjectEnvironmentCloneCheckpointObjectCredentialInputs{OperationID: "operation"}, SHA256: strings.Repeat("a", 64)}
	out, err := s.newCheckpointObjectCredentialValues(t.Context(), r)
	if err != nil || len(out.Preparations) != 0 || out.Endpoint != "" || out.SHA256 == "" {
		t.Fatal("empty credential matrix", err)
	}
}

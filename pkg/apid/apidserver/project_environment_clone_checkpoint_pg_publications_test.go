//go:build !no_pg

// adr: 590
package apidserver

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointPgPublicationFailureStore struct {
	*checkpointPgActivationFailureStore
	losePublish, loseWithdraw bool
}

func (s *checkpointPgPublicationFailureStore) PublishProjectEnvironmentCloneCheckpointPgBindings(ctx context.Context, l state.ProjectEnvironmentCloneLease, secrets []state.AppSecret, o state.ProjectEnvironmentCloneCheckpointPgActivationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPgPublication, error) {
	in, err := s.PgStore.PublishProjectEnvironmentCloneCheckpointPgBindings(ctx, l, secrets, o, r)
	if err == nil && s.losePublish {
		s.losePublish = false
		return state.ProjectEnvironmentCloneCheckpointPgPublication{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointPgPublicationFailureStore) WithdrawProjectEnvironmentCloneCheckpointPgBindings(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	err := s.PgStore.WithdrawProjectEnvironmentCloneCheckpointPgBindings(ctx, l)
	if err == nil && s.loseWithdraw {
		s.loseWithdraw = false
		return objectstorage.ErrUnavailable
	}
	return err
}
func checkpointPgPublicationFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointPgPublicationFailureStore, *checkpointPgActivationTestDriver, string) {
	t.Helper()
	f, store, d, _, id := checkpointPgActivationFixture(t)
	var err error
	f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return d.key.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return d.keys }
	oldHMAC := hostHMACKey
	hostHMACKey = func() []byte { return []byte("checkpoint-publication-test-key") }
	t.Cleanup(func() { hostHMACKey = oldHMAC })
	s := &checkpointPgPublicationFailureStore{checkpointPgActivationFailureStore: store}
	f.srv.store = statetest.NewPg(s)
	return f, s, d, id
}
func assertCheckpointPgPublication(t *testing.T, f *cloneCoordinatorFixture, s *checkpointPgPublicationFailureStore, want string) state.ProjectEnvironmentCloneCheckpointPgPublication {
	t.Helper()
	var raw []byte
	var status string
	if err := f.pool.QueryRow(t.Context(), "select plan,state from project_environment_clone_checkpoint_pg_publications where operation_id=$1", f.lease.Operation.ID).Scan(&raw, &status); err != nil || status != want {
		t.Fatal("publication journal", err)
	}
	var plan state.ProjectEnvironmentCloneCheckpointPgPublicationPlan
	// Use the typed read while capturing; compensation replay is checked by withdrawal.
	if want == "published" {
		inputs, err := s.PgStore.ProjectEnvironmentCloneCheckpointPgPublicationInputs(t.Context(), f.lease)
		if err != nil || inputs.Publication == nil {
			t.Fatal("publication replay", err)
		}
		plan = inputs.Publication.Plan
	}
	var bindings, secrets int
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_bindings b join managed_postgres_databases d on d.id=b.database_id where d.environment_clone_operation_id=$1 and b.state='ready'", f.lease.Operation.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), "select count(*) from app_secrets s join managed_postgres_bindings b on b.id=s.managed_postgres_binding_id join managed_postgres_databases d on d.id=b.database_id where d.environment_clone_operation_id=$1", f.lease.Operation.ID).Scan(&secrets); err != nil {
		t.Fatal(err)
	}
	expected := 0
	if want == "published" {
		expected = len(plan.Secrets)
	}
	if bindings != expected || secrets != expected {
		t.Fatal("partial PostgreSQL publication")
	}
	return state.ProjectEnvironmentCloneCheckpointPgPublication{Plan: plan, State: status}
}
func TestPGCloneCheckpointPgPublicationLostReplyAndKeylessRecovery(t *testing.T) {
	f, s, d, _ := checkpointPgPublicationFixture(t)
	original := f.lease.Operation
	s.losePublish = true
	var err error
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("expected lost publication reply", err)
	}
	before := assertCheckpointPgPublication(t, f, s, "published")
	inputs, err := s.PgStore.ProjectEnvironmentCloneCheckpointPgPublicationInputs(t.Context(), f.lease)
	if err != nil || len(inputs.Preparations) != len(before.Plan.Secrets) || len(inputs.Credentials) != 0 || len(inputs.Connections) != 0 {
		t.Fatal("committed workload preparations", err)
	}
	if inputs.BindingGraph == nil || len(inputs.BindingGraph.Bindings) != len(inputs.Preparations) || len(inputs.BindingGraph.SHA256) != 64 || inputs.BindingGraph.OperationID != f.lease.Operation.ID || inputs.BindingGraph.SourceRevisionHash != f.lease.Operation.SourceRevisionHash || inputs.BindingGraph.PublicationPlanSHA256 != inputs.Publication.PlanSHA256 {
		t.Fatal("binding graph lost original publication provenance")
	}
	for _, edge := range inputs.BindingGraph.Bindings {
		var actual state.ProjectEnvironmentClonePostgresBinding
		err := f.pool.QueryRow(t.Context(), `select b.id::text,b.database_id::text,d.name,b.environment_key,b.access,b.credential_ref,b.credential_generation,
d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.data_resource_id,d.region,d.postgres_major,d.service_class,d.availability,d.scale_to_zero,d.storage_limit_bytes,d.restore_window_seconds
from managed_postgres_bindings b join managed_postgres_databases d on d.id=b.database_id where b.id=$1`, edge.Target.ID).Scan(
			&actual.ID, &actual.DatabaseID, &actual.DatabaseName, &actual.EnvironmentKey, &actual.Access, &actual.CredentialRef, &actual.CredentialGeneration,
			&actual.BackendID, &actual.BackendFingerprint, &actual.ProviderResourceID, &actual.DataResourceID, &actual.Region, &actual.PostgresMajor, &actual.ServiceClass, &actual.Availability, &actual.ScaleToZero, &actual.StorageLimitBytes, &actual.RestoreWindowSeconds)
		if err != nil || !reflect.DeepEqual(actual, edge.Target) || edge.SourceBindingsSHA256 == "" || edge.SourceScope == "" {
			t.Fatal("projected binding graph differs from owned catalogue", err)
		}
	}
	for i, member := range inputs.Activation.Plan.Bindings {
		c := member.Connection.Credential
		prepared := inputs.Preparations[c.SourceBindingID]
		secret := before.Plan.Secrets[i]
		secret.ManagedPostgresAccess = "" // canonical workload preparation format
		if prepared.Binding.ID != c.TargetBindingID || prepared.Binding.DatabaseID != c.Runtime.Verification.Isolation.OwnerID || prepared.Hash == "" || !reflect.DeepEqual(prepared.Secret, secret) {
			t.Fatal("workload preparation changed original publication")
		}
	}
	for _, secret := range before.Plan.Secrets {
		values, err := secretbox.Open(d.key, secret.Ciphertext)
		if err != nil || len(values) != 1 || !strings.Contains(values[secret.Key], "sslmode=verify-full") {
			t.Fatal("unreadable managed secret", err)
		}
	}
	setSecretRecipient = nil
	mfaIdentities = nil
	hostHMACKey = nil
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal("keyless publication recovery", err)
	}
	after := assertCheckpointPgPublication(t, f, s, "published")
	recovered, err := s.PgStore.ProjectEnvironmentCloneCheckpointPgPublicationInputs(t.Context(), f.lease)
	if err != nil || !reflect.DeepEqual(inputs.Preparations, recovered.Preparations) || !reflect.DeepEqual(inputs.BindingGraph, recovered.BindingGraph) {
		t.Fatal("keyless handoff changed workload preparations", err)
	}
	if !reflect.DeepEqual(before.Plan, after.Plan) || d.activates != 1 || !reflect.DeepEqual(original.Resources, f.lease.Operation.Resources) {
		t.Fatal("recovery changed original ownership")
	}
	if _, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d); err != nil {
		t.Fatal("activation replay rejected its owned publication", err)
	}
}
func TestPGCloneCheckpointPgPublicationRollsBackCompleteSet(t *testing.T) {
	for _, kind := range []string{"second-owner", "quota", "collision", "late-journal", "health", "retention", "worker"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, _ := checkpointPgPublicationFixture(t)
			inputs, err := s.PgStore.ProjectEnvironmentCloneCheckpointPgPublicationInputs(t.Context(), f.lease)
			if err != nil {
				t.Fatal(err)
			}
			secrets := []state.AppSecret{}
			for i, member := range inputs.Activation.Plan.Bindings {
				secret, err := sealCheckpointPgAppSecret(member, inputs.Connections[i], inputs.Credentials[i])
				if err != nil {
					t.Fatal(err)
				}
				secrets = append(secrets, secret)
			}
			staleLease := f.lease
			switch kind {
			case "second-owner":
				secrets[1].ManagedCredentialGeneration = 2
			case "quota":
				account, err := s.PgStore.AccountByID(t.Context(), f.lease.Operation.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				max := api.MustLimitsFor(account.Plan).SecretCountMax
				if _, err = f.pool.Exec(t.Context(), "insert into app_secrets(account_id,app_id,scope,key,ciphertext) select $1,$2,'default','QUOTA_'||i,decode('01','hex') from generate_series(1,$3::int) i", secrets[1].AccountID, secrets[1].AppID, max); err != nil {
					t.Fatal(err)
				}
			case "collision":
				if _, err = f.pool.Exec(t.Context(), "insert into app_secrets(account_id,app_id,scope,key,ciphertext) values($1,$2,$3,$4,decode('01','hex'))", secrets[1].AccountID, secrets[1].AppID, secrets[1].Scope, secrets[1].Key); err != nil {
					t.Fatal(err)
				}
			case "late-journal":
				if _, err = f.pool.Exec(t.Context(), `create function reject_pg_publication() returns trigger language plpgsql as $$ begin raise exception 'late publication failure' using errcode='40001'; end $$;
create trigger reject_pg_publication before insert on project_environment_clone_checkpoint_pg_publications for each row execute function reject_pg_publication()`); err != nil {
					t.Fatal(err)
				}
			case "health":
				d.editActivation = func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.ExactGrantScope = false }
			case "retention":
				d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
					o.RetainedUntil = f.lease.ExpiresAt.Add(-1)
				}
			case "worker":
				reclaimCommonCheckpointWorker(t, f)
			}
			observed, err := d.ObserveCheckpointPgActivation(t.Context(), inputs.Activation.Plan, f.lease.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			lease := f.lease
			if kind == "worker" {
				lease = staleLease
			}
			if _, err = s.PublishProjectEnvironmentCloneCheckpointPgBindings(t.Context(), lease, secrets, observed, retention); err == nil {
				t.Fatal("invalid publication committed")
			}
			var count int
			if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_pg_publications where operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed publication retained a journal")
			}
			for _, member := range inputs.Activation.Plan.Bindings {
				if err = f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_bindings where id=$1", member.Connection.Credential.TargetBindingID).Scan(&count); err != nil || count != 0 {
					t.Fatal("partial binding escaped rollback")
				}
			}
		})
	}
}
func TestPGCloneCheckpointPgPublicationWithdrawalAndRetirement(t *testing.T) {
	f, s, d, _ := checkpointPgPublicationFixture(t)
	var err error
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	before := assertCheckpointPgPublication(t, f, s, "published")
	compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
	retirement := &checkpointCredentialRetirementTestDriver{checkpointCredentialTestDriver: d.checkpointCredentialTestDriver, receipts: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation)}
	s.loseWithdraw = true
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, retirement)
	if !errors.Is(err, objectstorage.ErrUnavailable) || retirement.retires != 0 {
		t.Fatal("retirement bypassed unknown withdrawal", err)
	}
	assertCheckpointPgPublication(t, f, s, "withdrawn")
	setSecretRecipient = nil
	mfaIdentities = nil
	hostHMACKey = nil
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, retirement)
	if err != nil || retirement.retires != len(before.Plan.Secrets) {
		t.Fatal("keyless withdrawal recovery", err)
	}
	cleanup := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
	if err != nil {
		t.Fatal("native target compensation", err)
	}
	if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
		t.Fatal("withdrawal replay after fork deletion", err)
	}
}
func TestPGCloneCheckpointPgPublicationRejectsChangedOwnedRows(t *testing.T) {
	for _, kind := range []string{"ciphertext", "scope", "binding", "foreign-reference"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, _ := checkpointPgPublicationFixture(t)
			var err error
			f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			before := assertCheckpointPgPublication(t, f, s, "published")
			secret := before.Plan.Secrets[0]
			// Only this disposable database bypasses the owner trigger to inject
			// corruption that ordinary SQL writers are already forbidden to create.
			if kind == "scope" || kind == "foreign-reference" {
				if _, err = f.pool.Exec(t.Context(), "alter table app_secrets disable trigger app_secret_managed_postgres_owner_guard"); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "ciphertext":
				_, err = f.pool.Exec(t.Context(), "update app_secrets set ciphertext=decode('02','hex') where managed_postgres_binding_id=$1", secret.ManagedPostgresBindingID)
			case "scope":
				_, err = f.pool.Exec(t.Context(), "update app_secrets set scope='foreign' where managed_postgres_binding_id=$1", secret.ManagedPostgresBindingID)
			case "binding":
				_, err = f.pool.Exec(t.Context(), "update managed_postgres_bindings set access='migration' where id=$1", secret.ManagedPostgresBindingID)
			case "foreign-reference":
				_, err = f.pool.Exec(t.Context(), "insert into app_secrets(account_id,app_id,scope,key,ciphertext,managed_postgres_binding_id,managed_credential_ref,managed_credential_generation) values($1,$2,$3,'FOREIGN_REFERENCE',decode('01','hex'),$4,$5,1)", secret.AccountID, secret.AppID, secret.Scope, secret.ManagedPostgresBindingID, secret.ManagedCredentialRef)
			}
			if kind == "scope" || kind == "foreign-reference" {
				if _, enableErr := f.pool.Exec(t.Context(), "alter table app_secrets enable trigger app_secret_managed_postgres_owner_guard"); enableErr != nil {
					t.Fatal(enableErr)
				}
			}
			if kind == "foreign-reference" {
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "23505" || pgerr.ConstraintName != "app_secrets_managed_postgres_binding_idx" {
					t.Fatal("extra reference bypassed unique ownership", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed publication replay accepted", err)
			}
			if _, err = s.PgStore.ProjectEnvironmentCloneCheckpointPgPublicationInputs(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed publication supplied workload preparations", err)
			}
			compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
				t.Fatal("withdrawal erased changed ownership", err)
			}
			var stateText string
			if err = f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_pg_publications where operation_id=$1", f.lease.Operation.ID).Scan(&stateText); err != nil || stateText != "published" {
				t.Fatal("failed withdrawal lost ownership")
			}
		})
	}
}
func TestPGCloneCheckpointPgPublicationMigrationGuards(t *testing.T) {
	f, s, d, _ := checkpointPgPublicationFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006343_clone_checkpoint_pg_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), query); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	before := assertCheckpointPgPublication(t, f, s, "published")
	for _, kind := range []string{"published", "withdrawn"} {
		if kind == "withdrawn" {
			compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
				t.Fatal(err)
			}
		}
		_, err = f.pool.Exec(t.Context(), parts[1])
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "clone_checkpoint_pg_publications_down_no_ownership" {
			t.Fatal("publication downgrade lost retained ownership", err)
		}
	}
	var retained []byte
	if err = f.pool.QueryRow(t.Context(), "select plan from project_environment_clone_checkpoint_pg_publications where operation_id=$1", f.lease.Operation.ID).Scan(&retained); err != nil || !bytes.Contains(retained, []byte(before.Plan.Secrets[0].Kid)) {
		t.Fatal("withdrawal lost retained secret evidence", err)
	}
}

func TestPGCloneCheckpointPgPublicationWithdrawalRollsBack(t *testing.T) {
	for _, kind := range []string{"late-write", "stale-worker"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, _ := checkpointPgPublicationFixture(t)
			var err error
			f.lease, err = f.srv.publishCheckpointPgBindings(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			before := assertCheckpointPgPublication(t, f, s, "published")
			compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
			lease := f.lease
			if kind == "late-write" {
				if _, err = f.pool.Exec(t.Context(), `create function reject_pg_withdrawal() returns trigger language plpgsql as $$ begin if NEW.state='withdrawn' then raise exception 'late withdrawal failure' using errcode='40001'; end if; return NEW; end $$;
create trigger reject_pg_withdrawal before update on project_environment_clone_checkpoint_pg_publications for each row execute function reject_pg_withdrawal()`); err != nil {
					t.Fatal(err)
				}
			} else {
				reclaimCommonCheckpointWorker(t, f)
			}
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), lease); err == nil {
				t.Fatal("invalid withdrawal committed")
			}
			for _, secret := range before.Plan.Secrets {
				var count int
				if err = f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where managed_postgres_binding_id=$1", secret.ManagedPostgresBindingID).Scan(&count); err != nil || count != 1 {
					t.Fatal("partial withdrawal escaped rollback")
				}
			}
			if kind == "late-write" {
				if _, err = f.pool.Exec(t.Context(), "drop trigger reject_pg_withdrawal on project_environment_clone_checkpoint_pg_publications; drop function reject_pg_withdrawal()"); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.WithdrawProjectEnvironmentCloneCheckpointPgBindings(t.Context(), f.lease); err != nil {
				t.Fatal("withdrawal recovery", err)
			}
			assertCheckpointPgPublication(t, f, s, "withdrawn")
		})
	}
}

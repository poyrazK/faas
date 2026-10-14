//go:build !no_pg

// adr: 590
package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointCredentialFailureStore struct {
	*checkpointRuntimeFailureStore
	loseSeal, loseCredentialClaim, loseCredentialFinish bool
}

func (s *checkpointCredentialFailureStore) SealProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l state.ProjectEnvironmentCloneLease, id, kid string, cipher []byte) (state.ProjectEnvironmentCloneCheckpointPostgresCredential, error) {
	in, err := s.PgStore.SealProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id, kid, cipher)
	if err == nil && s.loseSeal {
		s.loseSeal = false
		return state.ProjectEnvironmentCloneCheckpointPostgresCredential{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointCredentialFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, p state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof) (state.ProjectEnvironmentCloneCheckpointPostgresCredential, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id, p)
	if err == nil && s.loseCredentialClaim {
		s.loseCredentialClaim = false
		return state.ProjectEnvironmentCloneCheckpointPostgresCredential{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointCredentialFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, p state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id, o, p)
	if err == nil && s.loseCredentialFinish {
		s.loseCredentialFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointCredentialTestDriver struct {
	*checkpointRuntimeTestDriver
	key                    *age.X25519Identity
	prepares               int
	loseCredential         bool
	credentials            map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation
	keys                   []*age.X25519Identity
	bindingCount           int
	editCredential         func(*state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation)
	beforeCredentialFinish func()
	passwordBuffers        [][]byte // retained only to assert the caller zeroes memory
}

func (d *checkpointCredentialTestDriver) PrepareCheckpointPostgresCredential(ctx context.Context, r state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, password cloneCheckpointCredentialPassword) error {
	var status, kid string
	var cipher []byte
	if err := d.f.pool.QueryRow(ctx, "select state,key_id,ciphertext from project_environment_clone_checkpoint_postgres_credentials where operation_id=$1 and source_binding_id=$2", r.Plan.Runtime.Verification.Isolation.OperationID, r.Plan.SourceBindingID).Scan(&status, &kid, &cipher); err != nil || status != "requested" {
		d.t.Fatal("credential before sealed durable claim")
	}
	var key *age.X25519Identity
	for _, candidate := range d.keys {
		if candidate.Recipient().String() == kid {
			key = candidate
		}
	}
	if key == nil {
		d.t.Fatal("unknown credential recipient")
	}
	namespace, raw, err := secretbox.OpenBytes(key, cipher)
	if err != nil {
		d.t.Fatal("original envelope unreadable")
	}
	defer clear(raw)
	var envelope checkpointPostgresCredentialEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		d.t.Fatal("credential envelope")
	}
	defer clear(envelope.Password)
	if namespace != checkpointPostgresCredentialNamespace || envelope.PlanSHA256 != state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(r.Plan) || !bytes.Equal(envelope.Password, password.value) || len(password.value) == 0 || bytes.Contains(cipher, password.value) {
		d.t.Fatal("native credential differs from original encrypted material")
	}
	d.passwordBuffers = append(d.passwordBuffers, password.value)
	d.prepares++
	d.credentials[state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(r.Plan)] = state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(r.Plan), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(r.Plan), CiphertextSHA256: r.CiphertextSHA256, ProviderIdentityID: r.Plan.ProviderIdentityID, OwnerID: r.Plan.Runtime.Verification.Isolation.OwnerID, DataResourceID: r.Plan.DataResourceID, Access: r.Plan.Access, IdentityDisabled: true, PrivateScope: true, NoSourceAuthority: true, SQLScopeUnchanged: true, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseCredential {
		d.loseCredential = false
		return fmt.Errorf("provider echoed credential input: %s", password.value)
	}
	return nil
}
func (d *checkpointCredentialTestDriver) ObserveCheckpointPostgresCredential(_ context.Context, r state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, until time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, error) {
	out, ok := d.credentials[state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(r.Plan)]
	if !ok {
		return out, objectstorage.ErrNotFound
	}
	out.DisabledThrough = until.Add(time.Hour)
	if d.editCredential != nil {
		d.editCredential(&out)
	}
	if d.beforeCredentialFinish != nil {
		d.beforeCredentialFinish()
	}
	return out, nil
}
func checkpointCredentialFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointCredentialFailureStore, *checkpointCredentialTestDriver, string, string) {
	t.Helper()
	f, base, runtime, id := checkpointRuntimeFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, runtime)
	if err != nil {
		t.Fatal(err)
	}
	views, err := base.PgStore.ProjectEnvironmentCloneBindings(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindingID := ""
	count := 0
	for _, view := range views {
		for _, binding := range view.Postgres {
			if bindingID == "" {
				bindingID = binding.ID
			}
			count++
		}
	}
	if count < 1 {
		t.Fatal("fixture must have captured bindings", count)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient, oldIdentities := setSecretRecipient, mfaIdentities
	setSecretRecipient = func() *age.X25519Recipient { return key.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{key} }
	t.Cleanup(func() { setSecretRecipient, mfaIdentities = oldRecipient, oldIdentities })
	store := &checkpointCredentialFailureStore{checkpointRuntimeFailureStore: base}
	f.srv.store = statetest.NewPg(store)
	return f, store, &checkpointCredentialTestDriver{checkpointRuntimeTestDriver: runtime, key: key, keys: []*age.X25519Identity{key}, bindingCount: count, credentials: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation)}, bindingID, id
}
func assertCheckpointCredentialPrivate(t *testing.T, f *cloneCoordinatorFixture, s *checkpointCredentialFailureStore, d *checkpointCredentialTestDriver, bindingID, id, want string) state.ProjectEnvironmentCloneCheckpointPostgresCredential {
	t.Helper()
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, bindingID)
	if err != nil || in.State != want {
		t.Fatal("credential journal state", in.State, err)
	}
	var bindings, secrets int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_bindings where id=$1", in.Plan.TargetBindingID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where managed_postgres_binding_id=$1", in.Plan.TargetBindingID).Scan(&secrets); err != nil || bindings != 0 || secrets != 0 {
		t.Fatal("credential published before stage publication")
	}
	for _, buffer := range d.passwordBuffers {
		for _, b := range buffer {
			if b != 0 {
				t.Fatal("native plaintext retained after call")
			}
		}
	}
	assertCheckpointRuntimePrivate(t, f, s.checkpointRuntimeFailureStore, id, true)
	return in
}
func TestPGCloneCheckpointPostgresCredentialLostReplies(t *testing.T) {
	for _, kind := range []string{"seal", "native", "claim", "commit"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointCredentialFixture(t)
			switch kind {
			case "seal":
				s.loseSeal = true
			case "native":
				d.loseCredential = true
			case "claim":
				s.loseCredentialClaim = true
			case "commit":
				s.loseCredentialFinish = true
			}
			var err error
			f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if kind == "native" {
				if !errors.Is(err, errCloneCheckpointUnavailable) {
					t.Fatal("provider credential input crossed error boundary")
				}
			} else if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("expected lost reply", err)
			}
			want := "requested"
			if kind == "seal" {
				want = "sealed"
			}
			if kind == "commit" {
				want = "prepared"
			}
			original := assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, want)
			reclaimCommonCheckpointWorker(t, f)
			before := f.lease.Operation
			if _, err = f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", id); err != nil {
				t.Fatal(err)
			}
			f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if kind == "claim" {
				if !errors.Is(err, objectstorage.ErrNotFound) || d.prepares != 0 {
					t.Fatal("uncertain credential request reissued", err)
				}
				return
			}
			if err != nil || d.prepares != d.bindingCount || !reflect.DeepEqual(before, f.lease.Operation) {
				t.Fatal("credential recovery changed checkpoint or reissued identity", err)
			}
			prepared := assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "prepared")
			if !bytes.Equal(original.Ciphertext, prepared.Ciphertext) || original.Plan.TargetBindingID != prepared.Plan.TargetBindingID {
				t.Fatal("credential regenerated after handoff")
			}
			setSecretRecipient = nil
			mfaIdentities = nil
			f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil || d.prepares != d.bindingCount {
				t.Fatal("prepared recovery required secret or reissued identity", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresCredentialRejectsProof(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation){
		"owner": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) { o.OwnerID = "foreign" },
		"identity": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.ProviderIdentityID = "source"
		},
		"dataset": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.DataResourceID = "source"
		},
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.RequestID = "replacement"
		},
		"envelope": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.CiphertextSHA256 = strings.Repeat("d", 64)
		},
		"access": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) { o.Access = "foreign" },
		"enabled": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.IdentityDisabled = false
		},
		"source-authority": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.NoSourceAuthority = false
		},
		"sql-roster": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.SQLScopeUnchanged = false
		},
		"private": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) { o.PrivateScope = false },
		"horizon": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
			o.DisabledThrough = time.Now().Add(time.Minute)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointCredentialFixture(t)
			d.editCredential = edit
			var err error
			f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("incomplete credential proof accepted", err)
			}
			assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "requested")
			d.editCredential = nil
			f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil || d.prepares != d.bindingCount {
				t.Fatal("valid original credential proof reissued identity", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresCredentialRechecksPrerequisites(t *testing.T) {
	for _, kind := range []string{"closure", "runtime", "retention", "worker"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointCredentialFixture(t)
			d.beforeCredentialFinish = func() {
				d.beforeCredentialFinish = nil
				switch kind {
				case "closure":
					d.editIsolation = func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
						o.AllWritersClosed = false
					}
				case "runtime":
					d.editRuntime = func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.RuntimeReady = false }
				case "retention":
					d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
						o.RetainedUntil = time.Now().Add(time.Minute)
					}
				case "worker":
					reclaimCommonCheckpointWorker(t, f)
				}
			}
			if _, err := f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("lost prerequisite accepted", err)
			}
			assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "requested")
			d.editIsolation = nil
			d.editRuntime = nil
			d.editRelease = nil
			var err error
			f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil || d.prepares != d.bindingCount {
				t.Fatal("credential recovery reissued identity", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresCredentialSealRotationAndTampering(t *testing.T) {
	f, s, d, bindingID, id := checkpointCredentialFixture(t)
	s.loseSeal = true
	var err error
	f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	in := assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "sealed")
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	d.keys = append(d.keys, current)
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current} }
	if _, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, errCloneCheckpointUnavailable) || d.prepares != 0 {
		t.Fatal("missing original key claimed native identity")
	}
	altered := append([]byte(nil), in.Ciphertext...)
	altered[len(altered)-1] ^= 1
	if _, err = s.SealProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, bindingID, in.KeyID, altered); !errors.Is(err, state.ErrConflict) {
		t.Fatal("sealed envelope replaced")
	}
	if _, err = s.SealProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, bindingID, in.KeyID, make([]byte, api.PostgresCheckpointCredentialCiphertextMaxBytes+1)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("over-cap credential accepted")
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, d.key} }
	f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil || d.prepares != d.bindingCount {
		t.Fatal("old original envelope failed overlap recovery", err)
	}
	prepared := assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "prepared")
	if prepared.KeyID != in.KeyID || !bytes.Equal(prepared.Ciphertext, in.Ciphertext) {
		t.Fatal("rotation resealed original credential")
	}
}
func TestPGCloneCheckpointPostgresCredentialLateFailureAndMigration(t *testing.T) {
	f, s, d, bindingID, id := checkpointCredentialFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006330_clone_checkpoint_postgres_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	publication, err := migrations.FS.ReadFile("20261011232006343_clone_checkpoint_pg_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(publication), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	activation, err := migrations.FS.ReadFile("20261011232006339_clone_checkpoint_pg_activations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(activation), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	connection, err := migrations.FS.ReadFile("20261011232006336_clone_checkpoint_pg_connection_values.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(connection), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	retirement, err := migrations.FS.ReadFile("20261011232006333_clone_checkpoint_postgres_credential_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(retirement), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_prepared_credential() returns trigger language plpgsql as $$ begin if NEW.state='prepared' then raise exception 'injected late credential failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_prepared_credential before update on project_environment_clone_checkpoint_postgres_credentials for each row execute function reject_prepared_credential()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d); err == nil {
		t.Fatal("expected late receipt failure")
	}
	assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "requested")
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_postgres_credentials_down_no_ownership" {
		t.Fatal("downgrade lost credential authority", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_prepared_credential on project_environment_clone_checkpoint_postgres_credentials; drop function reject_prepared_credential()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil || d.prepares != d.bindingCount {
		t.Fatal("late failure recovery reissued credentials", err)
	}
}
func TestPGCloneCheckpointPostgresCredentialCleanupGuard(t *testing.T) {
	f, s, d, bindingID, id := checkpointCredentialFixture(t)
	// Even an undispatched encrypted identity has durable ownership.
	s.loseSeal = true
	if _, err := f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "sealed")
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = s.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	if _, err = s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("owned credentials allowed native fork retirement", err)
	}
}
func TestPGCloneCheckpointPostgresCredentialAccountQuotaBeforeIO(t *testing.T) {
	f, _, d, _, id := checkpointCredentialFixture(t)
	// Fill the account's bounded ledger storage with other planned ownership
	// rows. Their native authority is irrelevant to charging occupied slots.
	if _, err := f.pool.Exec(t.Context(), `insert into project_environment_clone_checkpoint_postgres_credentials(operation_id,source_binding_id,source_database_id,account_id,target_binding_id,plan)
 select $1,gen_random_uuid(),$2,$3,gen_random_uuid(),'{}'::jsonb from generate_series(1,$4::integer)`, f.lease.Operation.ID, id, f.lease.Operation.AccountID, api.PostgresCheckpointCredentialsPerAccountMax); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrQuotaExceeded) || d.prepares != 0 {
		t.Fatal("quota failed after native credential IO", err)
	}
}
func TestPGCloneCheckpointPostgresCredentialPasswordRedaction(t *testing.T) {
	secret := cloneCheckpointCredentialPassword{value: []byte("synthetic-test-password")}
	if strings.Contains(fmt.Sprintf("%v %#v", secret, secret), "synthetic-test-password") {
		t.Fatal("plaintext debug representation")
	}
	raw, err := json.Marshal(secret)
	if err != nil || bytes.Contains(raw, secret.value) {
		t.Fatal("plaintext JSON representation")
	}
}
func TestPGCloneCheckpointPostgresCredentialRejectsForeignSource(t *testing.T) {
	f, s, _, _, _ := checkpointCredentialFixture(t)
	if _, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign binding reserved credential", err)
	}
}

func TestPGCloneCheckpointPostgresCredentialRejectsUnboundEnvelope(t *testing.T) {
	for _, kind := range []string{"namespace", "plan", "length"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointCredentialFixture(t)
			in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, bindingID)
			if err != nil {
				t.Fatal(err)
			}
			envelope := checkpointPostgresCredentialEnvelope{PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(in.Plan), Password: []byte(strings.Repeat("a", 43))}
			namespace := checkpointPostgresCredentialNamespace
			switch kind {
			case "namespace":
				namespace = "foreign-credential"
			case "plan":
				envelope.PlanSHA256 = strings.Repeat("d", 64)
			case "length":
				envelope.Password = []byte("short")
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(raw)
			defer clear(envelope.Password)
			cipher, err := secretbox.SealBytes(d.key.Recipient(), namespace, raw, api.PostgresCheckpointCredentialCiphertextMaxBytes)
			if err != nil {
				t.Fatal("test seal failed")
			}
			if _, err = s.SealProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, bindingID, d.key.Recipient().String(), cipher); err != nil {
				t.Fatal(err)
			}
			if _, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, errCloneCheckpointUnavailable) || d.prepares != 0 {
				t.Fatal("unbound envelope reached provider")
			}
			assertCheckpointCredentialPrivate(t, f, s, d, bindingID, id, "sealed")
		})
	}
}

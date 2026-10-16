//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointCredentialRetirementFailureStore struct {
	*checkpointCredentialFailureStore
	loseReserve, loseClaim, loseFinish bool
}

func (s *checkpointCredentialRetirementFailureStore) ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx, l, id)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointCredentialRetirementFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx, l, id)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointCredentialRetirementFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointCredentialRetirementTestDriver struct {
	*checkpointCredentialTestDriver
	retires      int
	loseRetire   bool
	receipts     map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation
	edit         func(*state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation)
	beforeFinish func()
}

func (d *checkpointCredentialRetirementTestDriver) RetireCheckpointPostgresCredential(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_pg_credential_retirements where operation_id=$1 and source_binding_id=$2", p.Credential.Runtime.Verification.Isolation.OperationID, p.Credential.SourceBindingID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("retirement before durable claim")
	}
	id := state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementRequestID(p)
	if _, ok := d.receipts[id]; ok {
		d.t.Fatal("original retirement reissued")
	}
	d.retires++
	d.receipts[id] = state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation{RequestID: id, PreparationRequestID: state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(p.Credential), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlanSHA256(p), ProviderIdentityID: p.Credential.ProviderIdentityID, OwnerID: p.Credential.Runtime.Verification.Isolation.OwnerID, DataResourceID: p.Credential.DataResourceID, PreparationFenced: true, ActivationFenced: p.ActivationRequestID != "", IdentityAbsent: true, SessionsClosed: true, NoSourceAuthority: true, SQLScopeUnchanged: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseRetire {
		d.loseRetire = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointCredentialRetirementTestDriver) ObserveCheckpointPostgresCredentialRetirement(_ context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation, error) {
	out, ok := d.receipts[state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementRequestID(p)]
	if !ok {
		return out, objectstorage.ErrNotFound
	}
	if d.edit != nil {
		d.edit(&out)
	}
	if d.beforeFinish != nil {
		d.beforeFinish()
	}
	return out, nil
}
func compensateCheckpointCredentialFixture(t *testing.T, f *cloneCoordinatorFixture, s *checkpointCredentialFailureStore) {
	t.Helper()
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = s.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
}
func checkpointCredentialRetirementFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointCredentialRetirementFailureStore, *checkpointCredentialRetirementTestDriver, string, string) {
	t.Helper()
	f, s, d, bindingID, id := checkpointCredentialFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	compensateCheckpointCredentialFixture(t, f, s)
	store := &checkpointCredentialRetirementFailureStore{checkpointCredentialFailureStore: s}
	f.srv.store = statetest.NewPg(store)
	// Retirement and observation never require credential decryption.
	setSecretRecipient = nil
	mfaIdentities = nil
	return f, store, &checkpointCredentialRetirementTestDriver{checkpointCredentialTestDriver: d, receipts: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation)}, bindingID, id
}
func assertCheckpointCredentialRetirement(t *testing.T, f *cloneCoordinatorFixture, s *checkpointCredentialRetirementFailureStore, bindingID, id, want string) state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement {
	t.Helper()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(t.Context(), f.lease, bindingID)
	if err != nil || in.State != want {
		t.Fatal("retirement journal", in.State, err)
	}
	if want != "retired" {
		if _, err = s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unproven retirement allowed fork deletion", err)
		}
	}
	return in
}
func TestPGCloneCheckpointPostgresCredentialRetirementLostReplies(t *testing.T) {
	for _, kind := range []string{"reserve", "claim", "native", "commit"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointCredentialRetirementFixture(t)
			switch kind {
			case "reserve":
				s.loseReserve = true
			case "claim":
				s.loseClaim = true
			case "native":
				d.loseRetire = true
			case "commit":
				s.loseFinish = true
			}
			var err error
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if kind == "native" {
				if !errors.Is(err, errCloneCheckpointUnavailable) {
					t.Fatal("native retirement error", err)
				}
			} else if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("lost retirement reply", err)
			}
			want := "requested"
			if kind == "reserve" {
				want = "planned"
			}
			if kind == "commit" {
				want = "retired"
			}
			original := assertCheckpointCredentialRetirement(t, f, s, bindingID, id, want)
			if kind == "commit" && d.bindingCount > 1 {
				if _, err = s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
					t.Fatal("one retired binding released another binding's database", err)
				}
			}
			reclaimCommonCheckpointWorker(t, f)
			before := f.lease.Operation
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if kind == "claim" {
				if !errors.Is(err, objectstorage.ErrNotFound) || d.retires != 0 {
					t.Fatal("uncertain retirement reissued", err)
				}
				assertCheckpointCredentialRetirement(t, f, s, bindingID, id, "requested")
				return
			}
			if err != nil || d.retires != d.bindingCount || !reflect.DeepEqual(before, f.lease.Operation) {
				t.Fatal("retirement recovery changed original intent", err)
			}
			done := assertCheckpointCredentialRetirement(t, f, s, bindingID, id, "retired")
			if !reflect.DeepEqual(done.Plan, original.Plan) {
				t.Fatal("retirement replaced original preparation")
			}
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil || d.retires != d.bindingCount {
				t.Fatal("retired replay reissued", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresCredentialRetirementRejectsProof(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation){
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.RequestID = "foreign"
		},
		"preparation": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.PreparationRequestID = "foreign"
		},
		"plan": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.PlanSHA256 = strings.Repeat("f", 64)
		},
		"identity": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.ProviderIdentityID = "source"
		},
		"owner": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.OwnerID = "source"
		},
		"dataset": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.DataResourceID = "source"
		},
		"late-creation": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.PreparationFenced = false
		},
		"identity-present": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.IdentityAbsent = false
		},
		"sessions": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.SessionsClosed = false
		},
		"source-authority": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.NoSourceAuthority = false
		},
		"sql-roster": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.SQLScopeUnchanged = false
		},
		"old": func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
			o.RetiredAt = time.Unix(1, 0).UTC()
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointCredentialRetirementFixture(t)
			d.edit = edit
			var err error
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("invalid retirement accepted", err)
			}
			assertCheckpointCredentialRetirement(t, f, s, bindingID, id, "requested")
			d.edit = nil
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil || d.retires != d.bindingCount {
				t.Fatal("valid original retirement recovery", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresCredentialRetirementAllPreparationStates(t *testing.T) {
	for _, kind := range []string{"planned", "sealed", "requested", "prepared"} {
		t.Run(kind, func(t *testing.T) {
			f, s, base, bindingID, id := checkpointCredentialFixture(t)
			var err error
			switch kind {
			case "planned":
				_, err = s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, bindingID)
			case "sealed":
				s.loseSeal = true
				f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, base)
				if errors.Is(err, objectstorage.ErrUnavailable) {
					err = nil
				}
			case "requested":
				base.loseCredential = true
				f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, base)
				if errors.Is(err, errCloneCheckpointUnavailable) {
					err = nil
				}
			case "prepared":
				f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, base)
			}
			if err != nil {
				t.Fatal(err)
			}
			original := assertCheckpointCredentialPrivate(t, f, s, base, bindingID, id, kind)
			compensateCheckpointCredentialFixture(t, f, s)
			store := &checkpointCredentialRetirementFailureStore{checkpointCredentialFailureStore: s}
			f.srv.store = statetest.NewPg(store)
			d := &checkpointCredentialRetirementTestDriver{checkpointCredentialTestDriver: base, receipts: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation)}
			setSecretRecipient = nil
			mfaIdentities = nil
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			done := assertCheckpointCredentialRetirement(t, f, store, bindingID, id, "retired")
			if done.Plan.PreparationState != kind || !reflect.DeepEqual(done.Plan.Credential, original.Plan) || done.Plan.CiphertextSHA256 != original.CiphertextSHA256 {
				t.Fatal("retirement lost original ownership")
			}
			var cipher []byte
			var status string
			if err = f.pool.QueryRow(t.Context(), "select state,ciphertext from project_environment_clone_checkpoint_postgres_credentials where operation_id=$1 and source_binding_id=$2", f.lease.Operation.ID, bindingID).Scan(&status, &cipher); err != nil || status != kind || !reflect.DeepEqual(cipher, original.Ciphertext) {
				t.Fatal("retirement rewrote original credential")
			}
			cleanup := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
			if err != nil {
				t.Fatal("fork catalogue retirement", err)
			}
			before := d.retires
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
			if err != nil || d.retires != before {
				t.Fatal("retirement replay after fork deletion", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresCredentialRetirementWorkerFence(t *testing.T) {
	f, s, d, bindingID, id := checkpointCredentialRetirementFixture(t)
	stale := f.lease
	reclaimCommonCheckpointWorker(t, f)
	if _, _, err := s.ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(t.Context(), stale, bindingID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker retirement", err)
	}
	d.beforeFinish = func() { d.beforeFinish = nil; reclaimCommonCheckpointWorker(t, f) }
	if _, err := f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late worker replacement accepted", err)
	}
	assertCheckpointCredentialRetirement(t, f, s, bindingID, id, "requested")
	var err error
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil || d.retires != d.bindingCount {
		t.Fatal("handoff reissued retirement", err)
	}
}
func TestPGCloneCheckpointPostgresCredentialRetirementLateFailureAndMigration(t *testing.T) {
	f, s, d, bindingID, id := checkpointCredentialRetirementFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006333_clone_checkpoint_postgres_credential_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_retired_credential() returns trigger language plpgsql as $$ begin if NEW.state='retired' then raise exception 'injected late retirement failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_retired_credential before update on project_environment_clone_checkpoint_pg_credential_retirements for each row execute function reject_retired_credential()`); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err == nil {
		t.Fatal("late retirement failure ignored")
	}
	assertCheckpointCredentialRetirement(t, f, s, bindingID, id, "requested")
	if _, err = f.pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded retirement fence")
	}
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "clone_checkpoint_postgres_credential_retirements_down_no_ownership" {
		t.Fatal("wrong retirement downgrade guard", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_retired_credential on project_environment_clone_checkpoint_pg_credential_retirements; drop function reject_retired_credential()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil || d.retires != d.bindingCount {
		t.Fatal("late retirement recovery", err)
	}
	if _, err = f.pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded completed retirement evidence")
	}
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "clone_checkpoint_postgres_credential_retirements_down_no_ownership" {
		t.Fatal("wrong terminal retirement downgrade guard", err)
	}
}
func TestPGCloneCheckpointPostgresCredentialRetirementTerminalProofImmutable(t *testing.T) {
	f, s, d, bindingID, id := checkpointCredentialRetirementFixture(t)
	var err error
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	assertCheckpointCredentialRetirement(t, f, s, bindingID, id, "retired")
	d.edit = func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
		o.RetiredAt = o.RetiredAt.Add(-time.Microsecond)
	}
	if _, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminal retirement proof replaced", err)
	}
}

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

type checkpointPgActivationFailureStore struct {
	*checkpointPgConnectionFailureStore
	loseActivationReserve, loseActivationClaim, loseActivationFinish bool
}

func (s *checkpointPgActivationFailureStore) ReserveProjectEnvironmentCloneCheckpointPgActivation(ctx context.Context, l state.ProjectEnvironmentCloneLease, p []state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgActivation, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPgActivation(ctx, l, p)
	if err == nil && s.loseActivationReserve {
		s.loseActivationReserve = false
		return state.ProjectEnvironmentCloneCheckpointPgActivation{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointPgActivationFailureStore) ClaimProjectEnvironmentCloneCheckpointPgActivation(ctx context.Context, l state.ProjectEnvironmentCloneLease, p []state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgActivation, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPgActivation(ctx, l, p)
	if err == nil && s.loseActivationClaim {
		s.loseActivationClaim = false
		return state.ProjectEnvironmentCloneCheckpointPgActivation{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointPgActivationFailureStore) FinishProjectEnvironmentCloneCheckpointPgActivation(ctx context.Context, l state.ProjectEnvironmentCloneLease, o state.ProjectEnvironmentCloneCheckpointPgActivationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPgActivation(ctx, l, o, r)
	if err == nil && s.loseActivationFinish {
		s.loseActivationFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointPgActivationTestDriver struct {
	*checkpointPgConnectionTestDriver
	activates               int
	started, loseActivation bool
	activation              state.ProjectEnvironmentCloneCheckpointPgActivationObservation
	editActivation          func(*state.ProjectEnvironmentCloneCheckpointPgActivationObservation)
	beforeActivationFinish  func()
}

func (d *checkpointPgActivationTestDriver) ObserveCheckpointPostgresCredential(ctx context.Context, r state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, until time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, error) {
	if d.started {
		return state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation{}, state.ErrConflict
	}
	return d.checkpointCredentialTestDriver.ObserveCheckpointPostgresCredential(ctx, r, until)
}
func (d *checkpointPgActivationTestDriver) ObserveCheckpointPgEndpoint(ctx context.Context, r state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, until time.Time) (state.ProjectEnvironmentCloneCheckpointPgEndpointObservation, error) {
	if d.started {
		return state.ProjectEnvironmentCloneCheckpointPgEndpointObservation{}, state.ErrConflict
	}
	return d.checkpointPgConnectionTestDriver.ObserveCheckpointPgEndpoint(ctx, r, until)
}
func (d *checkpointPgActivationTestDriver) ActivateCheckpointPgCredentials(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPgActivationPlan) error {
	var status string
	var members int
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_pg_activations where operation_id=$1", p.OperationID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("activation before durable claim")
	}
	if err := d.f.pool.QueryRow(ctx, "select count(*) from project_environment_clone_checkpoint_pg_connection_values where activation_operation_id=$1 and state='sealed'", p.OperationID).Scan(&members); err != nil || members != len(p.Bindings) || members != d.bindingCount {
		d.t.Fatal("activation omitted a captured binding")
	}
	if d.started {
		d.t.Fatal("activation reissued")
	}
	d.started = true
	d.activates++
	d.activation = state.ProjectEnvironmentCloneCheckpointPgActivationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPgActivationRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPgActivationPlanSHA256(p), AllowedBindingsSHA256: state.ProjectEnvironmentCloneCheckpointPgActivationBindingsSHA256(p), AllIdentitiesActive: true, ExactGrantScope: true, NoSourceAuthority: true, InheritedAuthorityAbsent: true, ScheduledJobsDisabled: true, PrivateScope: true, EndpointsPinned: true, TLSVerified: true, DatasetsPinned: true, ActivatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseActivation {
		d.loseActivation = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointPgActivationTestDriver) ObserveCheckpointPgActivation(_ context.Context, _ state.ProjectEnvironmentCloneCheckpointPgActivationPlan, until time.Time) (state.ProjectEnvironmentCloneCheckpointPgActivationObservation, error) {
	if !d.started {
		return d.activation, objectstorage.ErrNotFound
	}
	out := d.activation
	out.HealthyThrough = until.Add(time.Hour)
	if d.editActivation != nil {
		d.editActivation(&out)
	}
	if d.beforeActivationFinish != nil {
		d.beforeActivationFinish()
	}
	return out, nil
}
func checkpointPgActivationFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointPgActivationFailureStore, *checkpointPgActivationTestDriver, string, string) {
	t.Helper()
	f, s, d, bindingID, id := checkpointPgConnectionFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointPgActivationFailureStore{checkpointPgConnectionFailureStore: s}
	f.srv.store = statetest.NewPg(store)
	// Activation and original intent recovery never require secret material.
	setSecretRecipient = nil
	mfaIdentities = nil
	return f, store, &checkpointPgActivationTestDriver{checkpointPgConnectionTestDriver: d}, bindingID, id
}
func assertCheckpointPgActivationPrivate(t *testing.T, f *cloneCoordinatorFixture, s *checkpointPgActivationFailureStore, want string) state.ProjectEnvironmentCloneCheckpointPgActivation {
	t.Helper()
	in, err := s.PgStore.ProjectEnvironmentCloneCheckpointPgActivationForLease(t.Context(), f.lease)
	if err != nil || in.State != want {
		t.Fatal("activation journal", in.State, err)
	}
	for _, b := range in.Plan.Bindings {
		var bindings, secrets int
		if err = f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_bindings where id=$1", b.Connection.Credential.TargetBindingID).Scan(&bindings); err != nil {
			t.Fatal(err)
		}
		if err = f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where managed_postgres_binding_id=$1", b.Connection.Credential.TargetBindingID).Scan(&secrets); err != nil || bindings != 0 || secrets != 0 {
			t.Fatal("activation published bindings or secrets")
		}
	}
	return in
}
func TestPGCloneCheckpointPgActivationLostReplies(t *testing.T) {
	for _, kind := range []string{"reserve", "claim", "native", "commit"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, _, id := checkpointPgActivationFixture(t)
			switch kind {
			case "reserve":
				s.loseActivationReserve = true
			case "claim":
				s.loseActivationClaim = true
			case "native":
				d.loseActivation = true
			case "commit":
				s.loseActivationFinish = true
			}
			var err error
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if kind == "native" {
				if !errors.Is(err, errCloneCheckpointUnavailable) {
					t.Fatal("native activation error", err)
				}
			} else if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("expected lost activation reply", err)
			}
			want := "requested"
			if kind == "reserve" {
				want = "planned"
			}
			if kind == "commit" {
				want = "active"
			}
			original := assertCheckpointPgActivationPrivate(t, f, s, want)
			reclaimCommonCheckpointWorker(t, f)
			before := f.lease.Operation
			if _, err = f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", id); err != nil {
				t.Fatal(err)
			}
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if kind == "claim" {
				if !errors.Is(err, objectstorage.ErrNotFound) || d.activates != 0 {
					t.Fatal("uncertain activation reissued", err)
				}
				assertCheckpointPgActivationPrivate(t, f, s, "requested")
				return
			}
			if err != nil || d.activates != 1 || !reflect.DeepEqual(before, f.lease.Operation) {
				t.Fatal("original activation recovery", err)
			}
			active := assertCheckpointPgActivationPrivate(t, f, s, "active")
			if !reflect.DeepEqual(original.Plan, active.Plan) {
				t.Fatal("activation changed original binding manifest")
			}
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if err != nil || d.activates != 1 {
				t.Fatal("active replay reissued activation", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgActivationRejectsProof(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPgActivationObservation){
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.RequestID = "replacement" },
		"plan": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) {
			o.PlanSHA256 = strings.Repeat("f", 64)
		},
		"scope": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) {
			o.AllowedBindingsSHA256 = strings.Repeat("f", 64)
		},
		"partial":          func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.AllIdentitiesActive = false },
		"extra-grants":     func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.ExactGrantScope = false },
		"source-authority": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.NoSourceAuthority = false },
		"inherited": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) {
			o.InheritedAuthorityAbsent = false
		},
		"jobs": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) {
			o.ScheduledJobsDisabled = false
		},
		"private":  func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.PrivateScope = false },
		"endpoint": func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.EndpointsPinned = false },
		"tls":      func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.TLSVerified = false },
		"dataset":  func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.DatasetsPinned = false },
		"health":   func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) { o.HealthyThrough = time.Now() },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, s, d, _, _ := checkpointPgActivationFixture(t)
			d.editActivation = edit
			var err error
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("incomplete activation scope accepted", err)
			}
			assertCheckpointPgActivationPrivate(t, f, s, "requested")
			d.editActivation = nil
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if err != nil || d.activates != 1 {
				t.Fatal("activation proof recovery reissued original intent", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgActivationRequiresCompleteOriginalManifest(t *testing.T) {
	for _, kind := range []string{"missing-proof", "duplicate-proof", "missing-value", "changed-endpoint"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, _ := checkpointPgActivationFixture(t)
			proofs, err := f.srv.checkpointPgInactiveActivationProofs(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			if len(proofs) < 2 {
				t.Fatal("fixture must contain multiple captured bindings")
			}
			switch kind {
			case "missing-proof":
				proofs = proofs[:1]
			case "duplicate-proof":
				proofs[1] = proofs[0]
			case "missing-value":
				if _, err = f.pool.Exec(t.Context(), "delete from project_environment_clone_checkpoint_pg_connection_values where operation_id=$1 and source_binding_id=$2", f.lease.Operation.ID, bindingID); err != nil {
					t.Fatal(err)
				}
			case "changed-endpoint":
				proofs[0].Endpoint.Host = "replacement.example"
			}
			if _, err = s.ReserveProjectEnvironmentCloneCheckpointPgActivation(t.Context(), f.lease, proofs); err == nil {
				t.Fatal("incomplete or replacement manifest reserved activation")
			}
			var count int
			if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_pg_activations where operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 0 || d.activates != 0 {
				t.Fatal("invalid manifest retained activation owner")
			}
		})
	}
}
func TestPGCloneCheckpointPgActivationRechecksBeforeClaim(t *testing.T) {
	f, s, d, _, _ := checkpointPgActivationFixture(t)
	proofs, err := f.srv.checkpointPgInactiveActivationProofs(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointPgActivation(t.Context(), f.lease, proofs); err != nil {
		t.Fatal(err)
	}
	proofs[0].Credential.IdentityDisabled = false
	if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointPgActivation(t.Context(), f.lease, proofs); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("changed dormant credential allowed dispatch", err)
	}
	assertCheckpointPgActivationPrivate(t, f, s, "planned")
	if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointPgActivation(t.Context(), f.lease, nil); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("claim omitted inactive prerequisite proofs", err)
	}
}
func TestPGCloneCheckpointPgActivationRechecksAfterIO(t *testing.T) {
	for _, kind := range []string{"worker", "retention"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, _, _ := checkpointPgActivationFixture(t)
			d.beforeActivationFinish = func() {
				d.beforeActivationFinish = nil
				if kind == "worker" {
					reclaimCommonCheckpointWorker(t, f)
				} else {
					d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) { o.RetainedUntil = time.Now() }
				}
			}
			if _, err := f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("late activation prerequisite loss accepted", err)
			}
			assertCheckpointPgActivationPrivate(t, f, s, "requested")
			d.editRelease = nil
			var err error
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if err != nil || d.activates != 1 {
				t.Fatal("activation handoff reissued intent", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgActivationLateFailureAndMigration(t *testing.T) {
	f, s, d, _, _ := checkpointPgActivationFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006339_clone_checkpoint_pg_activations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	publicationRaw, err := migrations.FS.ReadFile("20261011232006343_clone_checkpoint_pg_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(publicationRaw), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_active_checkpoint() returns trigger language plpgsql as $$ begin if NEW.state='active' then raise exception 'injected late activation failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_active_checkpoint before update on project_environment_clone_checkpoint_pg_activations for each row execute function reject_active_checkpoint()`); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
	if err == nil {
		t.Fatal("late activation failure ignored")
	}
	assertCheckpointPgActivationPrivate(t, f, s, "requested")
	if _, err = f.pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade lost requested activation owner")
	}
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "clone_checkpoint_pg_activations_down_no_ownership" {
		t.Fatal("wrong activation downgrade guard", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_active_checkpoint on project_environment_clone_checkpoint_pg_activations; drop function reject_active_checkpoint()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
	if err != nil || d.activates != 1 {
		t.Fatal("late activation recovery", err)
	}
	if _, err = f.pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded active scope evidence")
	}
}
func TestPGCloneCheckpointPgActivationRetirementFencesDelayedActivation(t *testing.T) {
	for _, kind := range []string{"planned", "unknown", "active"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointPgActivationFixture(t)
			if kind == "planned" {
				s.loseActivationReserve = true
			} else if kind == "unknown" {
				s.loseActivationClaim = true
			}
			var err error
			f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
			if kind == "active" && err != nil {
				t.Fatal(err)
			}
			if kind != "active" && !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("expected uncertain activation", err)
			}
			compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
			retire := &checkpointCredentialRetirementTestDriver{checkpointCredentialTestDriver: d.checkpointCredentialTestDriver, receipts: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation)}
			retire.edit = func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) {
				o.ActivationFenced = false
			}
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, retire)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("unfenced delayed activation allowed retirement", err)
			}
			owner, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(t.Context(), f.lease, bindingID)
			if err != nil || owner.State != "requested" || owner.Plan.ActivationRequestID == "" || owner.Plan.ActivationPlanSHA256 == "" {
				t.Fatal("retirement lost original activation intent", err)
			}
			if _, err = s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unfenced activation released fork", err)
			}
			retire.edit = nil
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, retire)
			if err != nil || retire.retires != d.bindingCount {
				t.Fatal("retirement fence recovery", err)
			}
			cleanup := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
			if err != nil {
				t.Fatal("native target compensation", err)
			}
			before := retire.retires
			f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, retire)
			if err != nil || retire.retires != before {
				t.Fatal("activation-fenced retirement replay after deletion", err)
			}
			if _, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("compensation redispatched activation", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgActivationTerminalProofImmutable(t *testing.T) {
	f, _, d, _, _ := checkpointPgActivationFixture(t)
	var err error
	f.lease, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	d.editActivation = func(o *state.ProjectEnvironmentCloneCheckpointPgActivationObservation) {
		o.ActivatedAt = o.ActivatedAt.Add(-time.Microsecond)
	}
	if _, err = f.srv.activateCheckpointPgCredentials(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminal activation receipt replaced", err)
	}
}

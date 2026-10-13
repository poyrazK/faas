//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointCaptureRetirementFailureStore struct {
	PgStore                            *state.PgStore
	loseReserve, loseClaim, loseFinish bool
}

func (s *checkpointCaptureRetirementFailureStore) ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return in, objectstorage.ErrUnavailable
	}
	return in, err
}

func (s *checkpointCaptureRetirementFailureStore) ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return in, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}

func (s *checkpointCaptureRetirementFailureStore) FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, o state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointCaptureRetirementTestDriver struct {
	t                       *testing.T
	f                       *cloneCoordinatorFixture
	calls, reads            int
	loseNative, loseObserve bool
	proof                   state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation
	beforeObserve           func()
	edit                    func(*state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation)
}

func checkpointCaptureRetirementProof(p state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation {
	s := p.SourceCompensation
	c := s.Plan.Capture
	o := state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointCaptureRetirementRequestID(c.OperationID), PlanSHA256: cloneCheckpointDigest(p), OwnerToken: c.OperationID, CaptureRequestID: c.OperationID, CapturePlanSHA256: cloneCheckpointDigest(c), CapturePermanentlyFenced: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond)}
	for _, source := range s.Observation.Sources {
		o.Sources = append(o.Sources, state.ProjectEnvironmentCloneCheckpointRetiredCapture{Kind: source.Kind, ID: source.ID, SourceResourceID: source.SourceResourceID, CaptureIdentity: source.CaptureIdentity, OwnerToken: source.OwnerToken, NoOwnedArtifacts: true, NoNativeBorrowers: true, SourceUnchanged: true, SourceAccessRestored: true})
	}
	return o
}

func (d *checkpointCaptureRetirementTestDriver) RetireCheckpointCapture(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "SELECT state FROM project_environment_clone_checkpoint_capture_retirements WHERE operation_id=$1", p.SourceCompensation.Plan.Capture.OperationID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("native capture retirement before durable claim", status, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		d.t.Fatal("native IO has no worker deadline")
	}
	d.calls++
	d.proof = checkpointCaptureRetirementProof(p)
	// Adapters receive independent copies of every nested plan/proof slice.
	p.SourceCompensation.Plan.Capture.Sources[0].ID = "driver mutation"
	p.SourceCompensation.Observation.Sources[0].ID = "driver mutation"
	if p.SourceCompensation.Plan.Receipt != nil {
		p.SourceCompensation.Plan.Receipt.Sources[0].CaptureIdentity = "driver mutation"
	}
	if d.loseNative {
		d.loseNative = false
		return objectstorage.ErrUnavailable
	}
	return nil
}

func (d *checkpointCaptureRetirementTestDriver) ObserveCheckpointCaptureRetirement(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) (state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation, error) {
	d.reads++
	if _, ok := ctx.Deadline(); !ok {
		d.t.Fatal("native observation has no worker deadline")
	}
	if d.beforeObserve != nil {
		d.beforeObserve()
	}
	if d.loseObserve {
		d.loseObserve = false
		return state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{}, objectstorage.ErrUnavailable
	}
	if d.proof.RequestID == "" {
		return state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{}, objectstorage.ErrNotFound
	}
	if d.proof.PlanSHA256 != cloneCheckpointDigest(p) {
		d.t.Fatal("adapter changed the immutable original plan")
	}
	o := d.proof
	o.Sources = append([]state.ProjectEnvironmentCloneCheckpointRetiredCapture(nil), o.Sources...)
	if d.edit != nil {
		d.edit(&o)
	}
	p.SourceCompensation.Plan.Capture.Sources[0].ID = "observer mutation"
	return o, nil
}

func checkpointCaptureRetirementFixture(t *testing.T, phase string) (*cloneCoordinatorFixture, *checkpointCaptureRetirementFailureStore, *checkpointCaptureRetirementTestDriver) {
	t.Helper()
	f, sourceStore, sourceDriver, p, _ := checkpointSourceCompensationFixture(t, phase)
	var err error
	f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, &p, sourceDriver)
	if err != nil {
		t.Fatal("source compensation", err)
	}
	store := &checkpointCaptureRetirementFailureStore{PgStore: sourceStore.PgStore}
	f.srv.store = statetest.NewPg(store)
	return f, store, &checkpointCaptureRetirementTestDriver{t: t, f: f}
}

func TestPGCloneCheckpointCaptureRetirementOriginalStatesAndReplay(t *testing.T) {
	for _, phase := range []string{"planned", "unknown-capture", "captured"} {
		t.Run(phase, func(t *testing.T) {
			f, store, d := checkpointCaptureRetirementFixture(t, phase)
			resources := cloneCheckpointDigest(f.lease.Operation.Resources)
			var err error
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
			if err != nil || d.calls != 1 || d.reads != 1 {
				t.Fatal("retirement", d.calls, d.reads, err)
			}
			in, err := store.PgStore.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(t.Context(), f.lease)
			if err != nil || in.State != "retired" || in.ObservationSHA256 != cloneCheckpointDigest(d.proof) {
				t.Fatal("terminal journal", err)
			}
			// Completed cleanup remains authentic without fresh provider IO.
			d.proof = state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{}
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
			if err != nil || d.calls != 1 || d.reads != 1 {
				t.Fatal("terminal replay performed native IO", d.calls, d.reads, err)
			}
			if f.lease.Operation.Status != state.CloneOperationCompensating || cloneCheckpointDigest(f.lease.Operation.Resources) != resources {
				t.Fatal("retirement changed the whole-stage operation")
			}
		})
	}
}

func TestPGCloneCheckpointCaptureRetirementLostRepliesAndHandoff(t *testing.T) {
	for _, fault := range []string{"reserve", "claim", "native", "observe", "finish"} {
		t.Run(fault, func(t *testing.T) {
			f, store, d := checkpointCaptureRetirementFixture(t, "captured")
			store.loseReserve, store.loseClaim, store.loseFinish = fault == "reserve", fault == "claim", fault == "finish"
			d.loseNative, d.loseObserve = fault == "native", fault == "observe"
			var err error
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
			if err == nil {
				t.Fatal("injected lost reply was ignored")
			}
			old := f.lease
			if _, err = f.pool.Exec(t.Context(), "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", old.Operation.ID); err != nil {
				t.Fatal(err)
			}
			f.lease, err = store.PgStore.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.srv.retireCheckpointCapture(t.Context(), old, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale worker entered recovery", err)
			}
			if fault == "claim" {
				// No acknowledged dispatch: even provider not-found cannot authorize
				// redispatch or terminal cleanup. Observe until original ownership
				// independently yields an authenticated terminal native result.
				f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
				if !errors.Is(err, objectstorage.ErrNotFound) || d.calls != 0 {
					t.Fatal("lost claim redispatched or fabricated absence", d.calls, err)
				}
				in, err := store.PgStore.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(t.Context(), f.lease)
				if err != nil || in.State != "requested" {
					t.Fatal(err)
				}
				d.proof = checkpointCaptureRetirementProof(in.Plan)
			}
			reads := d.reads
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
			wantCalls := 1
			if fault == "claim" {
				wantCalls = 0
			}
			if err != nil || d.calls != wantCalls {
				t.Fatal("handoff recovery", d.calls, err)
			}
			if fault == "finish" && d.reads != reads {
				t.Fatal("lost terminal reply caused new native IO")
			}
		})
	}
}

func TestPGCloneCheckpointCaptureRetirementRejectsAdoptedCaptures(t *testing.T) {
	f, _, d := checkpointCaptureRetirementFixture(t, "adopted")
	if _, err := f.srv.retireCheckpointCapture(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) || d.calls != 0 || d.reads != 0 {
		t.Fatal("adopted capture bypassed borrower retirement", d.calls, d.reads, err)
	}
}

func TestPGCloneCheckpointCaptureRetirementUnusedAdoptedCatalogue(t *testing.T) {
	f, store, d := checkpointCaptureRetirementFixture(t, "adopted")
	source, err := store.PgStore.ProjectEnvironmentCloneCheckpointSourceCompensationForLease(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range source.Plan.Capture.Sources {
		if original.Kind == "object_storage" {
			if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, original.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
	if err != nil || d.calls != 1 || d.reads != 1 {
		t.Fatal("unused adopted capture retirement", d.calls, d.reads, err)
	}
	d.proof = state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{}
	f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, d)
	if err != nil || d.calls != 1 || d.reads != 1 {
		t.Fatal("adopted terminal replay performed native IO", d.calls, d.reads, err)
	}
	if f.lease.Operation.Status != state.CloneOperationCompensating {
		t.Fatal("whole stage falsely completed")
	}
}

func TestPGCloneCheckpointCaptureRetirementAdmissionAndIncompleteProof(t *testing.T) {
	for _, fault := range []string{"admission", "pending-source", "native-proof", "stale-after-observe", "cancelled-after-observe"} {
		t.Run(fault, func(t *testing.T) {
			f, _, d := checkpointCaptureRetirementFixture(t, "unknown-capture")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch fault {
			case "admission":
				f.srv.cloneWorkerAdmission = func(context.Context) error { return objectstorage.ErrUnavailable }
			case "pending-source":
				if _, err := f.pool.Exec(ctx, "UPDATE project_environment_clone_checkpoint_source_compensations SET state='requested',observation=NULL,observation_sha256=NULL,compensated_at=NULL WHERE operation_id=$1", f.lease.Operation.ID); err != nil {
					t.Fatal(err)
				}
			case "native-proof":
				d.edit = func(o *state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) {
					o.Sources[0].NoOwnedArtifacts = false
				}
			case "stale-after-observe":
				d.beforeObserve = func() {
					if _, err := f.pool.Exec(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", f.lease.Operation.ID); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled-after-observe":
				d.beforeObserve = cancel
			}
			if _, err := f.srv.retireCheckpointCapture(ctx, f.lease, d); err == nil {
				t.Fatal("invalid native retirement completed")
			}
			if (fault == "admission" || fault == "pending-source") && (d.calls != 0 || d.reads != 0) {
				t.Fatal("unavailable authority allowed native IO")
			}
			var completed int
			if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM project_environment_clone_checkpoint_capture_retirements WHERE operation_id=$1 AND state='retired'", f.lease.Operation.ID).Scan(&completed); err != nil || completed != 0 {
				t.Fatal("invalid proof recorded", completed, err)
			}
		})
	}
}

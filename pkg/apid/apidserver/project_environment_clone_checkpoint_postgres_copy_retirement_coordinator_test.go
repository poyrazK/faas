//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointPgCopyRetirementFailureStore struct {
	PgStore                            *state.PgStore
	loseReserve, loseClaim, loseFinish bool
}

func (s *checkpointPgCopyRetirementFailureStore) ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return in, errCloneCheckpointUnavailable
	}
	return in, err
}
func (s *checkpointPgCopyRetirementFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return in, false, errCloneCheckpointUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointPgCopyRetirementFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return errCloneCheckpointUnavailable
	}
	return err
}

type checkpointPgCopyRetirementTestDriver struct {
	t                       *testing.T
	f                       *cloneCoordinatorFixture
	calls, reads            int
	loseNative, loseObserve bool
	proof                   state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation
	edit                    func(*state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation)
	beforeFinish            func()
}

func (d *checkpointPgCopyRetirementTestDriver) RetireCheckpointPostgresCopy(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) error {
	var status string
	var leaseUntil time.Time
	if err := d.f.pool.QueryRow(ctx, "select r.state,o.lease_until from project_environment_clone_checkpoint_pg_copy_retirements r join project_environment_clone_operations o on o.id=r.operation_id where r.operation_id=$1 and r.source_database_id=$2", p.Scope.OperationID, p.Scope.SourceDatabaseID).Scan(&status, &leaseUntil); err != nil || status != "requested" {
		d.t.Fatal("native IO before committed claim", status, err)
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(leaseUntil) {
		d.t.Fatal("native IO lacks worker deadline")
	}
	d.calls++
	d.proof = checkpointPgCopyRetirementProof(p)
	// Deliberately corrupt all nested adapter inputs; observation gets a new copy.
	if p.Reader != nil {
		p.Reader.OwnerID = "adapter mutation"
	}
	if p.Target != nil {
		p.Target.TargetDatabaseID = "adapter mutation"
	}
	p.Capture.Isolation.Plan.NativeResourceID = "adapter mutation"
	if d.loseNative {
		d.loseNative = false
		return errCloneCheckpointUnavailable
	}
	return nil
}
func (d *checkpointPgCopyRetirementTestDriver) ObserveCheckpointPostgresCopyRetirement(_ context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation, error) {
	d.reads++
	if p.Reader != nil && p.Reader.OwnerID == "adapter mutation" || p.Target != nil && p.Target.TargetDatabaseID == "adapter mutation" || p.Capture.Isolation.Plan.NativeResourceID == "adapter mutation" {
		d.t.Fatal("adapter mutated saved plan")
	}
	if d.loseObserve {
		d.loseObserve = false
		return d.proof, errCloneCheckpointUnavailable
	}
	o := d.proof
	if d.edit != nil {
		d.edit(&o)
	}
	if d.beforeFinish != nil {
		d.beforeFinish()
	}
	return o, nil
}

func TestPGCloneCheckpointPgCopyRetirementCoordinatorRecovery(t *testing.T) {
	for _, fault := range []string{"none", "reserve", "claim", "native", "observe", "finish"} {
		t.Run(fault, func(t *testing.T) {
			f, base, id := checkpointPgCopyRetirementFixture(t, "requested", "requested")
			store := &checkpointPgCopyRetirementFailureStore{PgStore: base, loseReserve: fault == "reserve", loseClaim: fault == "claim", loseFinish: fault == "finish"}
			f.srv.store = statetest.NewPg(store)
			d := &checkpointPgCopyRetirementTestDriver{t: t, f: f, loseNative: fault == "native", loseObserve: fault == "observe"}
			var err error
			f.lease, err = f.srv.retireCheckpointPostgresCopies(t.Context(), f.lease, d)
			if fault == "none" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("lost reply accepted")
				}
				// A lost claim grants no dispatch authority to this or a replacement worker.
				if fault == "claim" {
					in, e := base.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(t.Context(), f.lease, id)
					if e != nil {
						t.Fatal(e)
					}
					d.proof = checkpointPgCopyRetirementProof(in.Plan)
				}
				reclaimCommonCheckpointWorker(t, f)
				f.lease, err = f.srv.retireCheckpointPostgresCopies(t.Context(), f.lease, d)
				if err != nil {
					t.Fatal("recovery", err)
				}
			}
			wantCalls := 1
			if fault == "claim" {
				wantCalls = 0
			}
			if d.calls != wantCalls {
				t.Fatalf("native calls=%d want=%d", d.calls, wantCalls)
			}
			in, err := base.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(t.Context(), f.lease, id)
			if err != nil || in.State != "retired" {
				t.Fatal("terminal journal", in.State, err)
			}
			calls, reads := d.calls, d.reads
			d.proof = state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation{}
			f.lease, err = f.srv.retireCheckpointPostgresCopies(t.Context(), f.lease, d)
			if err != nil || calls != d.calls || reads != d.reads {
				t.Fatal("terminal replay performed native IO", err)
			}
		})
	}
}

func TestPGCloneCheckpointPgCopyRetirementCoordinatorRejectsProofAndStaleWorker(t *testing.T) {
	for _, fault := range []string{"reader", "writer", "source", "digest", "handoff", "deadline"} {
		t.Run(fault, func(t *testing.T) {
			f, base, id := checkpointPgCopyRetirementFixture(t, "observed", "prepared")
			f.srv.store = base
			d := &checkpointPgCopyRetirementTestDriver{t: t, f: f}
			d.edit = func(o *state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) {
				switch fault {
				case "reader":
					o.AllReaderOperationsPermanentlyClosed = false
				case "writer":
					o.AllWritersPermanentlyClosed = false
				case "source":
					o.SourceUnchanged = false
				case "digest":
					o.PlanSHA256 = "changed"
				}
			}
			if fault == "handoff" {
				d.beforeFinish = func() { reclaimCommonCheckpointWorker(t, f) }
			}
			if fault == "deadline" {
				// Cancellation after observation must leave the committed request recoverable.
				ctx, cancel := context.WithCancel(t.Context())
				d.beforeFinish = cancel
				_, err := f.srv.retireCheckpointPostgresCopies(ctx, f.lease, d)
				if err == nil {
					t.Fatal("cancelled worker accepted")
				}
			} else {
				_, err := f.srv.retireCheckpointPostgresCopies(t.Context(), f.lease, d)
				if !errors.Is(err, state.ErrConflict) {
					t.Fatal("invalid proof/worker accepted", err)
				}
			}
			in, err := base.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(t.Context(), f.lease, id)
			if err != nil || in.State != "requested" {
				t.Fatal("failed finish changed journal", in.State, err)
			}
			d.edit = nil
			d.beforeFinish = nil
			f.lease, err = f.srv.retireCheckpointPostgresCopies(t.Context(), f.lease, d)
			if err != nil || d.calls != 1 {
				t.Fatal("observe-only recovery", d.calls, err)
			}
		})
	}
}

func TestCheckpointPgCopyRetirementPlanCopy(t *testing.T) {
	p := state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan{Reader: &state.ProjectEnvironmentClonePostgresCopyReader{OwnerID: "original"}, Target: &state.ProjectEnvironmentClonePostgresCopyTarget{TargetDatabaseID: "original"}, Capture: state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan{Isolation: &state.ProjectEnvironmentCloneCheckpointPostgresIsolation{}}}
	p.Reader.ObservedAt = time.Now()
	copied := cloneCheckpointPostgresCopyRetirementPlanCopy(p)
	copied.Reader.OwnerID = "changed"
	copied.Target.TargetDatabaseID = "changed"
	copied.Capture.Isolation.Plan.NativeResourceID = "changed"
	if p.Reader.OwnerID != "original" || p.Target.TargetDatabaseID != "original" || p.Capture.Isolation.Plan.NativeResourceID != "" {
		t.Fatal("copy aliases original owners")
	}
}

func TestPGCloneCheckpointPgCopyRetirementCoordinatorBorrowerStates(t *testing.T) {
	for _, pair := range [][2]string{{"reserved", "reserved"}, {"observed", "prepared"}, {"retired", "retired"}, {"requested", ""}, {"", "requested"}, {"", ""}} {
		t.Run(pair[0]+"-"+pair[1], func(t *testing.T) {
			f, base, _ := checkpointPgCopyRetirementFixture(t, pair[0], pair[1])
			f.srv.store = base
			d := &checkpointPgCopyRetirementTestDriver{t: t, f: f}
			var err error
			f.lease, err = f.srv.retireCheckpointPostgresCopies(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if pair[0] == "" && pair[1] == "" {
				expected = 0
			}
			if d.calls != expected || d.reads != expected {
				t.Fatal("unexpected native IO", d.calls, d.reads)
			}
		})
	}
}

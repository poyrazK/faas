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

type checkpointWorkloadRetirementFailureStore struct {
	PgStore                            *state.PgStore
	loseReserve, loseClaim, loseFinish bool
}

func (s *checkpointWorkloadRetirementFailureStore) ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return in, errCloneCheckpointUnavailable
	}
	return in, err
}
func (s *checkpointWorkloadRetirementFailureStore) ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return in, false, errCloneCheckpointUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointWorkloadRetirementFailureStore) FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, o state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return errCloneCheckpointUnavailable
	}
	return err
}

type checkpointWorkloadRetirementTestDriver struct {
	t                       *testing.T
	f                       *cloneCoordinatorFixture
	calls, reads            int
	loseNative, loseObserve bool
	proof                   state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation
	edit                    func(*state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation)
	beforeFinish            func()
}

func (d *checkpointWorkloadRetirementTestDriver) RetireCheckpointWorkloads(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) error {
	var status string
	var leaseUntil time.Time
	if err := d.f.pool.QueryRow(ctx, "select r.state,o.lease_until from project_environment_clone_checkpoint_workload_retirements r join project_environment_clone_operations o on o.id=r.operation_id where r.operation_id=$1", p.OperationID).Scan(&status, &leaseUntil); err != nil || status != "requested" {
		d.t.Fatal("native IO before committed claim", status, err)
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(leaseUntil) {
		d.t.Fatal("native IO lacks worker deadline")
	}
	d.calls++
	d.proof = checkpointWorkloadRetirementProof(p)
	if len(p.Workloads) > 0 {
		p.Workloads[0].AppID = "adapter mutation"
	}
	if d.loseNative {
		d.loseNative = false
		return errCloneCheckpointUnavailable
	}
	return nil
}
func (d *checkpointWorkloadRetirementTestDriver) ObserveCheckpointWorkloadRetirement(_ context.Context, p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation, error) {
	d.reads++
	if len(p.Workloads) > 0 && p.Workloads[0].AppID == "adapter mutation" {
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

func TestPGCloneCheckpointWorkloadRetirementCoordinatorRecovery(t *testing.T) {
	for _, fault := range []string{"none", "reserve", "claim", "native", "observe", "finish"} {
		t.Run(fault, func(t *testing.T) {
			f, captured, _ := checkpointCaptureRetirementFixture(t, "captured")
			base := captured.PgStore
			store := &checkpointWorkloadRetirementFailureStore{PgStore: base, loseReserve: fault == "reserve", loseClaim: fault == "claim", loseFinish: fault == "finish"}
			f.srv.store = statetest.NewPg(store)
			d := &checkpointWorkloadRetirementTestDriver{t: t, f: f, loseNative: fault == "native", loseObserve: fault == "observe"}
			var err error
			f.lease, err = f.srv.retireCheckpointWorkloads(t.Context(), f.lease, d)
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
					in, e := base.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(t.Context(), f.lease)
					if e != nil {
						t.Fatal(e)
					}
					d.proof = checkpointWorkloadRetirementProof(in.Plan)
				}
				reclaimCommonCheckpointWorker(t, f)
				f.lease, err = f.srv.retireCheckpointWorkloads(t.Context(), f.lease, d)
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
			in, err := base.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(t.Context(), f.lease)
			if err != nil || in.State != "retired" {
				t.Fatal("terminal journal", in.State, err)
			}
			calls, reads := d.calls, d.reads
			d.proof = state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{}
			f.lease, err = f.srv.retireCheckpointWorkloads(t.Context(), f.lease, d)
			if err != nil || calls != d.calls || reads != d.reads {
				t.Fatal("terminal replay performed native IO", err)
			}
		})
	}
}

func TestPGCloneCheckpointWorkloadRetirementCoordinatorRejectsProofAndStaleWorker(t *testing.T) {
	for _, fault := range []string{"scheduler", "runtime", "source", "digest", "handoff", "deadline"} {
		t.Run(fault, func(t *testing.T) {
			f, captured, _ := checkpointCaptureRetirementFixture(t, "captured")
			base := captured.PgStore
			f.srv.store = base
			d := &checkpointWorkloadRetirementTestDriver{t: t, f: f}
			d.edit = func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
				switch fault {
				case "scheduler":
					o.AllSchedulerOperationsClosed = false
				case "runtime":
					o.NoOwnedInstances = false
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
				_, err := f.srv.retireCheckpointWorkloads(ctx, f.lease, d)
				if err == nil {
					t.Fatal("cancelled worker accepted")
				}
			} else {
				_, err := f.srv.retireCheckpointWorkloads(t.Context(), f.lease, d)
				if !errors.Is(err, state.ErrConflict) {
					t.Fatal("invalid proof/worker accepted", err)
				}
			}
			in, err := base.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(t.Context(), f.lease)
			if err != nil || in.State != "requested" {
				t.Fatal("failed finish changed journal", in.State, err)
			}
			d.edit = nil
			d.beforeFinish = nil
			f.lease, err = f.srv.retireCheckpointWorkloads(t.Context(), f.lease, d)
			if err != nil || d.calls != 1 {
				t.Fatal("observe-only recovery", d.calls, err)
			}
		})
	}
}

func checkpointWorkloadRetirementProof(p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation {
	o := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(p), OwnerToken: p.OperationID, AllDeploymentRequestsPermanentlyFenced: true, AllSchedulerOperationsClosed: true, NoOwnedInstances: true, NoOwnedCgroups: true, NoOwnedNetworkResources: true, NoOwnedRoutes: true, NoNativePublication: true, SourceUnchanged: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond), Workloads: []state.ProjectEnvironmentCloneCheckpointRetiredWorkload{}}
	for _, w := range p.Workloads {
		o.Workloads = append(o.Workloads, state.ProjectEnvironmentCloneCheckpointRetiredWorkload{AppID: w.AppID, TargetDeploymentID: w.TargetDeploymentID, DeploymentPermanentlyFenced: w.TargetDeploymentID != "", AllRuntimeOperationsClosed: w.TargetDeploymentID != ""})
	}
	return o
}
func TestCheckpointWorkloadRetirementPlanCopy(t *testing.T) {
	for _, workloads := range [][]state.ProjectEnvironmentCloneCheckpointRetirementWorkload{nil, {}} {
		empty := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan{Workloads: workloads}
		if state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(empty) != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(cloneCheckpointWorkloadRetirementPlanCopy(empty)) {
			t.Fatal("copy changed empty membership digest")
		}
	}

	p := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan{Workloads: []state.ProjectEnvironmentCloneCheckpointRetirementWorkload{{AppID: "original"}}}
	copied := cloneCheckpointWorkloadRetirementPlanCopy(p)
	copied.Workloads[0].AppID = "changed"
	if p.Workloads[0].AppID != "original" {
		t.Fatal("copy aliases original workload owners")
	}
}

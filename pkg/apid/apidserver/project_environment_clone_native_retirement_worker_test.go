// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type nativeRetirementWorkerTestStore struct {
	*terminalCompensationWorkerTestStore
	native    state.ProjectEnvironmentCloneCheckpointWorkloadRetirement
	loseClaim bool
}

func (s *nativeRetirementWorkerTestStore) RenewProjectEnvironmentCloneLease(ctx context.Context, l state.ProjectEnvironmentCloneLease, ttl time.Duration) (state.ProjectEnvironmentCloneLease, error) {
	l, err := s.terminalCompensationWorkerTestStore.RenewProjectEnvironmentCloneLease(ctx, l, ttl)
	if err != nil {
		return l, err
	}
	// Exercise propagation of the CURRENT renewed revision into every subsequent
	// native and metadata mutation instead of assuming renewal is a no-op.
	l.Operation.Revision++
	s.lease = l
	return l, nil
}
func (s *nativeRetirementWorkerTestStore) ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	if err := s.step(ctx, l, "native_reserve"); err != nil {
		return state.ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, err
	}
	return s.native, nil
}
func (s *nativeRetirementWorkerTestStore) ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, bool, error) {
	if err := s.step(ctx, l, "native_claim"); err != nil {
		return state.ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, false, err
	}
	dispatch := s.native.State == "planned"
	if dispatch {
		s.native.State = "requested"
	}
	if s.loseClaim {
		s.loseClaim = false
		return state.ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, false, errors.New("test claim reply lost")
	}
	return s.native, dispatch, nil
}
func (s *nativeRetirementWorkerTestStore) FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, o state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error {
	if err := s.step(ctx, l, "native_finish"); err != nil {
		return err
	}
	if !o.NoOwnedInstances || o.OwnerToken != s.lease.Operation.ID || o.PlanSHA256 != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(s.native.Plan) {
		return state.ErrConflict
	}
	s.native.State, s.native.Observation = "retired", o
	return nil
}

type nativeRetirementWorkerTestDriver struct {
	store        *nativeRetirementWorkerTestStore
	fail         string
	calls, reads int
}

func (d *nativeRetirementWorkerTestDriver) RetireCheckpointWorkloads(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) error {
	d.calls++
	if d.store.native.State != "requested" {
		d.store.t.Fatal("native dispatch before durable claim")
	}
	if err := d.store.step(ctx, d.store.lease, "native_dispatch"); err != nil {
		return err
	}
	if d.fail == "dispatch" {
		return errors.New("test native request reply lost")
	}
	// Mutating the adapter's copy must not change the frozen journal or the plan
	// subsequently passed to the independent observer.
	p.Workloads[0].AppID = "adapter mutation"
	return nil
}
func (d *nativeRetirementWorkerTestDriver) ObserveCheckpointWorkloadRetirement(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation, error) {
	d.reads++
	if err := d.store.step(ctx, d.store.lease, "native_observe"); err != nil {
		return state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{}, err
	}
	if !reflect.DeepEqual(p, d.store.native.Plan) {
		d.store.t.Fatal("observer lost original identities")
	}
	if d.fail == "observe" {
		return state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{}, errors.New("test observation unavailable")
	}
	o := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{OwnerToken: p.OperationID, PlanSHA256: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(p), NoOwnedInstances: true}
	if d.fail == "proof" {
		o.NoOwnedInstances = false
	}
	return o, nil
}

func nativeRetirementWorkerFixture(t *testing.T) (*server, *nativeRetirementWorkerTestStore, *nativeRetirementWorkerTestDriver) {
	t.Helper()
	srv, base := terminalCompensationWorkerFixture(t)
	store := &nativeRetirementWorkerTestStore{terminalCompensationWorkerTestStore: base, native: state.ProjectEnvironmentCloneCheckpointWorkloadRetirement{
		State: "planned", Plan: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan{OperationID: base.lease.Operation.ID, AccountID: base.lease.Operation.AccountID, ProjectID: base.lease.Operation.ProjectID,
			Workloads: []state.ProjectEnvironmentCloneCheckpointRetirementWorkload{{AppID: uuid.NewString(), TargetDeploymentID: uuid.NewString()}}},
	}}
	driver := &nativeRetirementWorkerTestDriver{store: store}
	srv.store, srv.cloneWorkloadRetirementDriver = statetest.NewPg(store), driver
	return srv, store, driver
}

func TestCloneNativeRetirementWorkerCompletesBeforeMetadataCleanup(t *testing.T) {
	srv, store, driver := nativeRetirementWorkerFixture(t)
	if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(store.events, ",")
	if driver.calls != 1 || driver.reads != 1 || !store.committed || strings.Contains(joined, "release") || strings.Index(joined, "native_finish") > strings.Index(joined, "configuration") {
		t.Fatal("unverified native closure reached metadata cleanup", store.events)
	}
	if store.lease.Operation.Revision != 9 {
		t.Fatal("renewed native lease was not propagated", store.lease)
	}
}

func TestCloneNativeRetirementWorkerDefersUncertainOrIncompleteClosure(t *testing.T) {
	for _, fault := range []string{"native_reserve", "native_claim", "dispatch", "observe", "proof", "native_finish"} {
		t.Run(fault, func(t *testing.T) {
			srv, store, driver := nativeRetirementWorkerFixture(t)
			if strings.HasPrefix(fault, "native_") {
				store.failStep = fault
			} else {
				driver.fail = fault
			}
			if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); err == nil {
				t.Fatal("failed native retirement accepted")
			}
			if store.committed || strings.Contains(strings.Join(store.events, ","), "configuration") || store.events[len(store.events)-1] != "release" {
				t.Fatal("cleanup after unverified native closure", store.events)
			}
		})
	}
}

func TestCloneNativeRetirementWorkerRecoversWithoutRedispatch(t *testing.T) {
	for _, fault := range []string{"claim_reply", "dispatch_reply", "already_retired"} {
		t.Run(fault, func(t *testing.T) {
			srv, store, driver := nativeRetirementWorkerFixture(t)
			switch fault {
			case "claim_reply":
				store.loseClaim = true
			case "dispatch_reply":
				driver.fail = "dispatch"
			case "already_retired":
				store.native.State = "retired"
				store.native.Observation = state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{OwnerToken: store.lease.Operation.ID, PlanSHA256: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(store.native.Plan), NoOwnedInstances: true}
			}
			if fault != "already_retired" {
				if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); err == nil {
					t.Fatal("lost native reply accepted")
				}
				if store.committed {
					t.Fatal("unknown native closure completed operation")
				}
				driver.fail = ""
				store.events = nil
			}
			if err := srv.processNextProjectEnvironmentClone(t.Context(), statetest.NewPg(store)); err != nil {
				t.Fatal("retirement recovery failed", err)
			}
			wantCalls := 0
			if fault == "dispatch_reply" {
				wantCalls = 1
			}
			wantReads := 1
			if fault == "already_retired" {
				wantReads = 0
			}
			if driver.calls != wantCalls || driver.reads != wantReads || !store.committed {
				t.Fatal("native request redispatched or stored proof bypassed", driver.calls, driver.reads, store.events)
			}
		})
	}
}

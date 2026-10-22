package sched_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Fault-injection storage only. PostgreSQL tests cover authoritative fencing.
type drainTestStore struct {
	*state.MemStore
	drain    state.ManagedPostgresDrain
	instance state.Instance
	writeErr error
	reserved int
}

func (s *drainTestStore) ReserveManagedPostgresDrain(_ context.Context, cutover, instance, owner string) (state.ManagedPostgresDrain, error) {
	s.reserved++
	if cutover != s.drain.Frame.CutoverID || instance != s.drain.Frame.InstanceID || owner != s.drain.Frame.OwnerNodeID {
		return state.ManagedPostgresDrain{}, state.ErrConflict
	}
	return s.drain, nil
}
func (s *drainTestStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	if id != s.instance.ID {
		return state.Instance{}, state.ErrNotFound
	}
	return s.instance, nil
}

func (s *drainTestStore) ManagedPostgresDrain(context.Context, string) (state.ManagedPostgresDrain, error) {
	return s.drain, nil
}
func (s *drainTestStore) ListPendingManagedPostgresDrains(_ context.Context, _, after string, _ int) ([]state.ManagedPostgresDrain, error) {
	if s.drain.Proof != nil || after != "" {
		return nil, nil
	}
	return []state.ManagedPostgresDrain{s.drain}, nil
}
func (s *drainTestStore) CompleteManagedPostgresDrain(ctx context.Context, f state.ManagedPostgresDrainFrame, p state.ManagedPostgresDrainProof) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	if !f.Equal(s.drain.Frame) || !p.ValidFor(f) {
		return state.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.instance.State = "stopped"
	s.drain.Proof = &p
	return nil
}

type drainLegacyVMM struct {
	sched.RoutedVMM
	destroys int
}

func (v *drainLegacyVMM) Destroy(context.Context, string, string) error {
	v.destroys++
	return errors.New("unexpected generic destroy")
}

type drainTestVMM struct {
	*drainLegacyVMM
	proof      state.ManagedPostgresDrainProof
	err        error
	wrongFrame bool
	calls      int
}

func (v *drainTestVMM) RetireManagedPostgresDrain(_ context.Context, f state.ManagedPostgresDrainFrame) (sched.ManagedPostgresDrainEvidence, error) {
	v.calls++
	if v.wrongFrame {
		f.WakeID = uuid.NewString()
	}
	return sched.ManagedPostgresDrainEvidence{Frame: f, Proof: v.proof}, v.err
}

func drainSchedulerFixture(t *testing.T) (*sched.Engine, *drainTestStore, *drainTestVMM) {
	t.Helper()
	s := &drainTestStore{MemStore: state.NewMemStore()}
	v := &drainTestVMM{drainLegacyVMM: &drainLegacyVMM{}}
	e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), v, nil, "test-fc", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e.WithOwnerNodeID(uuid.NewString())
	f := state.ManagedPostgresDrainFrame{ID: uuid.NewString(), CutoverID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(),
		InstanceID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), OwnerNodeID: (*e.XOwnerNodeID()), OwnerRevision: 3, FencedAt: time.Now().UTC()}
	s.instance = state.Instance{ID: f.InstanceID, AppID: f.AppID, DeploymentID: f.DeploymentID, NodeID: f.NodeID, WakeID: f.WakeID, State: "running", RAMMB: 256}
	s.drain.Frame = f
	v.proof = state.ManagedPostgresDrainProof{DrainID: f.ID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
	if err := (*e.XLedger()).Admit(sched.Request{Instance: f.InstanceID, AppID: f.AppID, DeploymentID: f.DeploymentID, Plan: api.PlanHobby, RAMMB: 256, VCPU: 1, MaxConcurrency: 2, NodeID: f.NodeID}); err != nil {
		t.Fatal(err)
	}
	return e, s, v
}

func TestManagedPostgresDrainUncertaintyRetainsCapacityUntilRecoveryCommit(t *testing.T) {
	for _, failure := range []string{"unreachable", "cleanup", "wrong_frame", "database_write"} {
		t.Run(failure, func(t *testing.T) {
			e, s, v := drainSchedulerFixture(t)
			f := s.drain.Frame
			switch failure {
			case "unreachable":
				v.err = errors.New("node unreachable")
			case "cleanup":
				v.proof.ResourcesRemoved = false
			case "wrong_frame":
				v.wrongFrame = true
			case "database_write":
				s.writeErr = errors.New("commit unavailable")
			}
			if _, err := e.DrainManagedPostgresCutoverInstance(t.Context(), f.CutoverID, f.InstanceID); err == nil {
				t.Fatal("uncertainty accepted")
			}
			ins, err := s.InstanceByID(t.Context(), f.InstanceID)
			if err != nil || ins.State != "running" || s.drain.Proof != nil || (*e.XLedger()).ResidentRAM() != 256+api.PerVMOverheadMB || v.destroys != 0 {
				t.Fatal("failed drain released reservation", ins, err)
			}
			v.err, v.wrongFrame, s.writeErr = nil, false, nil
			v.proof.ResourcesRemoved = true
			page, err := e.RecoverManagedPostgresDrains(t.Context(), f.OwnerNodeID, "", 1)
			if err != nil || page.Retired != 1 || s.drain.Proof == nil || (*e.XLedger()).ResidentRAM() != 0 {
				t.Fatal("recovery failed", page, err)
			}
			calls := v.calls
			if _, err := e.DrainManagedPostgresCutoverInstance(t.Context(), f.CutoverID, f.InstanceID); err != nil || v.calls != calls {
				t.Fatal("committed receipt replayed stop", err)
			}
		})
	}
}

func TestManagedPostgresDrainNeverFallsBackToGenericDestroy(t *testing.T) {
	e, s, v := drainSchedulerFixture(t)
	(*e.Vmm()) = v.drainLegacyVMM
	f := s.drain.Frame
	if _, err := e.DrainManagedPostgresCutoverInstance(t.Context(), f.CutoverID, f.InstanceID); !errors.Is(err, state.ErrConflict) || s.reserved != 0 || v.destroys != 0 {
		t.Fatal("legacy capability reached effects", err)
	}
	page, err := e.RecoverManagedPostgresDrains(t.Context(), f.OwnerNodeID, "", 1)
	if err == nil || page.NextCursor != f.ID || (*e.XLedger()).ResidentRAM() == 0 {
		t.Fatal("legacy recovery lost reservation or cursor", page, err)
	}
	page, err = e.RecoverManagedPostgresDrains(t.Context(), f.OwnerNodeID, page.NextCursor, 1)
	if err != nil || page.NextCursor != "" {
		t.Fatal("unavailable member pinned cursor", page, err)
	}
}

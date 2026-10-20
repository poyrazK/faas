package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type drainPlanTestStore struct {
	*drainTestStore
	plan         state.ManagedPostgresDrainPlan
	reservations int
}

func (s *drainPlanTestStore) ReserveManagedPostgresDrainPlan(_ context.Context, cutover, owner string) (state.ManagedPostgresDrainPlan, error) {
	if cutover != s.plan.Frame.CutoverID || owner != s.plan.Frame.OwnerNodeID {
		return state.ManagedPostgresDrainPlan{}, state.ErrConflict
	}
	s.reservations++
	return s.plan, nil
}

func (s *drainPlanTestStore) ManagedPostgresDrainPlan(_ context.Context, id string) (state.ManagedPostgresDrainPlan, error) {
	if id != s.plan.Frame.ID {
		return state.ManagedPostgresDrainPlan{}, state.ErrNotFound
	}
	return s.plan, nil
}

func TestManagedPostgresDrainPlanQueuesIntentWithoutPhysicalEffects(t *testing.T) {
	e, base, v := drainSchedulerFixture(t)
	f := base.drain.Frame
	s := &drainPlanTestStore{drainTestStore: base, plan: state.ManagedPostgresDrainPlan{
		Frame: state.ManagedPostgresDrainPlanFrame{ID: f.ID, CutoverID: f.CutoverID, AppID: f.AppID,
			OwnerNodeID: f.OwnerNodeID, OwnerRevision: f.OwnerRevision, FencedAt: f.FencedAt},
		State: state.ManagedPostgresDrainPlanDraining, InventoryCount: 1, Pending: 1,
	}}
	(*e.XStore()) = s
	plan, err := e.PlanManagedPostgresCutoverDrain(t.Context(), f.CutoverID)
	if err != nil || plan.Frame.ID != f.ID || s.reservations != 1 || v.calls != 0 || v.destroys != 0 || base.drain.Proof != nil {
		t.Fatal("plan reservation performed a physical stop", plan, err)
	}
	if _, err := e.ManagedPostgresCutoverDrainPlan(t.Context(), plan.Frame.ID); err != nil || s.reservations != 1 || v.calls != 0 {
		t.Fatal("status inspection created work", err)
	}
	// A lost native reply remains ordinary pending receipt recovery. Recovery
	// never re-reserves the plan or replaces its immutable membership.
	v.err = errors.New("native reply lost")
	if _, err := e.RecoverManagedPostgresDrains(t.Context(), f.OwnerNodeID, "", 1); err == nil || base.drain.Proof != nil || (*e.XLedger()).ResidentRAM() == 0 {
		t.Fatal("lost reply completed the plan", err)
	}
	v.err = nil
	page, err := e.RecoverManagedPostgresDrains(t.Context(), f.OwnerNodeID, "", 1)
	if err != nil || page.Retired != 1 || base.drain.Proof == nil || s.reservations != 1 || (*e.XLedger()).ResidentRAM() != 0 {
		t.Fatal("existing receipt recovery did not converge", page, err)
	}
}

func TestManagedPostgresDrainPlanRequiresStoreAndNativeCapability(t *testing.T) {
	e, base, v := drainSchedulerFixture(t)
	if _, err := e.PlanManagedPostgresCutoverDrain(t.Context(), base.drain.Frame.CutoverID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("store without plan support accepted", err)
	}
	s := &drainPlanTestStore{drainTestStore: base}
	(*e.XStore()), (*e.Vmm()) = s, v.drainLegacyVMM
	if _, err := e.PlanManagedPostgresCutoverDrain(t.Context(), base.drain.Frame.CutoverID); !errors.Is(err, state.ErrConflict) || s.reservations != 0 || v.destroys != 0 {
		t.Fatal("legacy host accepted a drain plan", err)
	}
}

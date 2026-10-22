// adr: 1523
package sched

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type coldBootPersistenceStore struct {
	MemStore                    *state.MemStore
	failures, calls             int
	loseReply, invalidateAtBind bool
}

func (s *coldBootPersistenceStore) ExecuteOperatorColdBoot(ctx context.Context, id, token string) (state.OperatorColdBootResult, error) {
	s.calls++
	if s.failures > 0 {
		s.failures--
		return state.OperatorColdBootResult{}, errors.New("database unavailable")
	}
	r, err := s.MemStore.ExecuteOperatorColdBoot(ctx, id, token)
	if err == nil && s.loseReply {
		s.loseReply = false
		return state.OperatorColdBootResult{}, errors.New("commit reply lost")
	}
	return r, err
}

// CreateInstanceWithMode lands the operator cold boot between the wake's
// snapshot selection and its binding, which the admission insert makes
// (ADR-1741): invalidateAtBind fires on the first restore admission.
func (s *coldBootPersistenceStore) CreateInstanceWithMode(ctx context.Context, appID, deploymentID, current string, ramMB int, nodeID, wakeID, mode string) (state.Instance, error) {
	if s.invalidateAtBind && current == string(state.StateWaking) {
		s.invalidateAtBind = false
		if _, err := s.MemStore.InvalidateSnapshotRecovery(ctx, deploymentID); err != nil {
			return state.Instance{}, err
		}
	}
	return s.MemStore.CreateInstanceWithMode(ctx, appID, deploymentID, current, ramMB, nodeID, wakeID, mode)
}

func (s *coldBootPersistenceStore) BindInstanceSnapshotRecovery(ctx context.Context, id, wake, snapshot string) error {
	if s.invalidateAtBind && snapshot != "" {
		s.invalidateAtBind = false
		instance, err := s.MemStore.InstanceByID(ctx, id)
		if err != nil {
			return err
		}
		if _, err := s.MemStore.InvalidateSnapshotRecovery(ctx, instance.DeploymentID); err != nil {
			return err
		}
	}
	return s.MemStore.BindInstanceSnapshotRecovery(ctx, id, wake, snapshot)
}

func coldBootLoopFixture(t *testing.T) (*Loop, *coldBootPersistenceStore, state.OperatorIntent, state.App, state.Deployment, []string) {
	t.Helper()
	fake := &coldBootPersistenceStore{MemStore: state.NewMemStore()}
	store := statetest.NewMem(fake)
	acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	var ids []string
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snap, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", Tier: tier, StorageKey: state.SnapshotCaptureMemKey(dep.ID, tier, uuid.NewString()), MemBytes: 256 << 20})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, snap.ID)
	}
	if _, err := store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceColdBoot, dep.ID, &acct.ID, acct.ID, "recover cache", nil, nil); err != nil {
		t.Fatal(err)
	}
	intent, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	return loop, fake, intent, app, dep, ids
}

func TestOperatorColdBootDispatcherRecoversLostCommitReply(t *testing.T) {
	loop, store, intent, _, _, ids := coldBootLoopFixture(t)
	store.loseReply = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Dispatch has already claimed the row. Cancellation cannot orphan a
	// successful mutation between its result and the audit handoff.
	loop.processOperatorIntent(ctx, intent)
	row, err := store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
	if err != nil || row.Status != state.OperatorIntentSucceeded || row.ColdBootGeneration != 1 || !slices.Equal(row.SnapIDsMarkedStale, ids) || store.calls != 2 {
		t.Fatalf("lost reply: %+v calls=%d err=%v", row, store.calls, err)
	}
	freshLoop := &Loop{engine: newEngine(t, statetest.NewMem(store), &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	freshLoop.drainPendingOperatorIntents(t.Context())
	if store.calls != 2 {
		t.Fatal("fresh scheduler redispatched completed cold boot")
	}
	events, err := store.MemStore.ListEvents(t.Context(), *intent.AccountID, 100)
	if err != nil || len(events) != 1 {
		t.Fatalf("outcomes=%+v err=%v", events, err)
	}
}

func TestOperatorColdBootDispatcherRetriesAfterWorkerRestart(t *testing.T) {
	loop, store, intent, _, dep, ids := coldBootLoopFixture(t)
	store.failures = api.OperatorColdBootPersistenceAttempts
	loop.processOperatorIntent(t.Context(), intent)
	row, err := store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
	if err != nil || row.Status != state.OperatorIntentRunning || row.ColdBootGeneration != 0 {
		t.Fatalf("failed persistence reported completion: %+v %v", row, err)
	}
	latest, err := store.MemStore.LatestSnapshot(t.Context(), dep.ID)
	if err != nil || latest.ID != ids[0] {
		t.Fatalf("failed transaction changed snapshots: %+v %v", latest, err)
	}
	if _, err := store.MemStore.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	freshLoop := &Loop{engine: newEngine(t, statetest.NewMem(store), &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	freshLoop.drainPendingOperatorIntents(t.Context())
	row, err = store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
	if err != nil || row.Status != state.OperatorIntentSucceeded || row.ColdBootGeneration != 1 || !slices.Equal(row.SnapIDsMarkedStale, ids) {
		t.Fatalf("recovered result: %+v %v", row, err)
	}
}

func TestOperatorColdBootWakeColdThenPublishesReusableGeneration(t *testing.T) {
	loop, store, intent, app, dep, _ := coldBootLoopFixture(t)
	loop.processOperatorIntent(t.Context(), intent)
	vmm := loop.engine.vmm.(*fakeVMM)
	result, err := loop.engine.Wake(t.Context(), app.ID, "", "", "")
	if err != nil || vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Fatalf("wake=%+v cold=%d restores=%d err=%v", result, vmm.coldBoots, vmm.restores, err)
	}
	instance, err := store.MemStore.InstanceByID(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	init, err := store.MemStore.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0", MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "healthy")}, instance.ID, instance.StartedAt)
	if err != nil || init.RecoveryGeneration != 1 {
		t.Fatalf("healthy capture: %+v %v", init, err)
	}
	if err := loop.engine.Park(t.Context(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.engine.Wake(t.Context(), app.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if vmm.coldBoots != 1 || vmm.restores != 1 {
		t.Fatalf("healthy cache never reused: cold=%d restores=%d", vmm.coldBoots, vmm.restores)
	}
}

func TestOperatorColdBootSelectionRaceStopsBeforeVMDispatch(t *testing.T) {
	loop, store, _, app, _, _ := coldBootLoopFixture(t)
	store.invalidateAtBind = true
	if _, err := loop.engine.Wake(t.Context(), app.ID, "", "", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale selection admitted: %v", err)
	}
	vmm := loop.engine.vmm.(*fakeVMM)
	if vmm.coldBoots != 0 || vmm.restores != 0 || loop.engine.ledger.ResidentRAM() != 0 {
		t.Fatal("refused selection dispatched VM or retained admission")
	}
	if _, err := loop.engine.Wake(t.Context(), app.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Fatalf("retry restored suspect snapshot: cold=%d restore=%d", vmm.coldBoots, vmm.restores)
	}
}

func TestOperatorColdBootDiscardsOlderWarmPool(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	target := 1
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{WarmPoolSize: &target, SetWarmPoolSize: true}); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := store.CreateInstanceWithMode(t.Context(), app.ID, dep.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	warm = publishWarmFixture(t, store, warm)
	vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.ForceColdBootNextWake(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	result, err := e.Wake(t.Context(), app.ID, "", "", "")
	if err != nil || result.InstanceID == warm.ID || vmm.resumeCalls != 0 || vmm.coldBoots != 1 {
		t.Fatalf("warm bypass: result=%+v resume=%d cold=%d err=%v", result, vmm.resumeCalls, vmm.coldBoots, err)
	}
}

func TestOperatorColdBootDiscardsOlderFrozenInstance(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
		t.Fatalf("freeze: handled=%v err=%v", handled, err)
	}
	if _, err := f.e.ForceColdBootNextWake(t.Context(), f.dep.ID); err != nil {
		t.Fatal(err)
	}
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil || result.InstanceID == f.ins.ID || f.vmm.thaws != 0 || f.vmm.coldBoots != 1 {
		t.Fatalf("frozen bypass: result=%+v thaws=%d cold=%d err=%v", result, f.vmm.thaws, f.vmm.coldBoots, err)
	}
}

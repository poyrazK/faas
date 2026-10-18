// adr: 1522
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type restartPersistenceStore struct {
	MemStore            *state.MemStore
	failures            int
	lostAcknowledgement bool
	saves               int
}

type restartTerminalWriteFailure struct{ Store state.Store }

func (s *restartTerminalWriteFailure) UpdateInstanceStateToTerminal(context.Context, string, string, time.Time) error {
	return errors.New("terminal state write failed")
}

func TestForceRestartPropagatesTerminalWriteFailure(t *testing.T) {
	mem := state.NewMemStore()
	store := statetest.NewStore(&restartTerminalWriteFailure{Store: mem})
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	instance := runningInstance(t, store, app, dep, vmm, engine)
	if _, err := engine.ForceRestart(t.Context(), instance.ID, "test"); err == nil {
		t.Fatal("terminal write failure reported success")
	}
	if !engine.ledger.ResidentFor(instance.ID) {
		t.Fatal("failed durable transition released admission")
	}
}

func (s *restartPersistenceStore) CompleteOperatorRestart(ctx context.Context, completion state.OperatorRestartCompletion) (state.OperatorRestartResult, error) {
	s.saves++
	if s.failures > 0 {
		s.failures--
		return state.OperatorRestartResult{}, errors.New("database unavailable")
	}
	result, err := s.MemStore.CompleteOperatorRestart(ctx, completion)
	if err == nil && s.lostAcknowledgement {
		s.lostAcknowledgement = false
		return state.OperatorRestartResult{}, errors.New("completion acknowledgement lost")
	}
	return result, err
}

type restartProtocolVMM struct {
	*fakeVMM
	store     state.OperatorRestartStore
	rpcs      int
	loseReply bool
	interrupt bool
}

func (v *restartProtocolVMM) OperatorRestartGeneration(context.Context, string, string) (string, error) {
	return "physical-generation", nil
}
func (v *restartProtocolVMM) ExecuteOperatorRestartDestroy(ctx context.Context, nodeID, id, token string) error {
	v.rpcs++
	e, execute, err := v.store.StartOperatorRestartDestroy(ctx, id, token, nodeID)
	if err != nil {
		return err
	}
	if execute {
		v.destroys++
		if v.interrupt {
			return errors.New("vmmd connection lost during teardown")
		}
		if err := v.store.AcknowledgeOperatorRestartDestroy(ctx, id, e.DestroyToken); err != nil {
			return err
		}
	}
	if v.loseReply {
		return errors.New("RPC acknowledgement lost")
	}
	return nil
}

func restartLoopFixture(t *testing.T) (*Loop, *restartPersistenceStore, *restartProtocolVMM, state.OperatorIntent, state.Instance) {
	t.Helper()
	fakeStore := &restartPersistenceStore{MemStore: state.NewMemStore()}
	store := statetest.NewMem(fakeStore)
	account, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	vmm := &restartProtocolVMM{fakeVMM: &fakeVMM{}, store: store}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	engine.Ledger().Admit(Request{Instance: instance.ID, AppID: app.ID, Plan: api.PlanPro, RAMMB: 512, VCPU: 2, MaxConcurrency: 5})
	_, err = store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceRestart, instance.ID, &account.ID, account.ID, "restart", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return &Loop{engine: engine, log: testLog()}, fakeStore, vmm, intent, instance
}

func TestOperatorRestartSchedulerRecovery(t *testing.T) {
	t.Run("changed_prepared_target_fails_without_dispatch", func(t *testing.T) {
		loop, store, vmm, intent, instance := restartLoopFixture(t)
		if _, err := loop.prepareOperatorRestart(t.Context(), store.MemStore, intent); err != nil {
			t.Fatal(err)
		}
		if err := store.MemStore.UpdateInstanceState(t.Context(), instance.ID, string(state.StateParked)); err != nil {
			t.Fatal(err)
		}
		if err := loop.processOperatorRestart(t.Context(), intent); err != nil {
			t.Fatal(err)
		}
		row, err := store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
		if err != nil || row.Status != state.OperatorIntentFailed || vmm.rpcs != 0 || vmm.destroys != 0 {
			t.Fatalf("changed target = %+v, %v, RPCs=%d", row, err, vmm.rpcs)
		}
	})
	t.Run("lost_rpc_and_completion_acknowledgements_do_not_repeat_teardown", func(t *testing.T) {
		loop, store, vmm, intent, instance := restartLoopFixture(t)
		vmm.loseReply, store.lostAcknowledgement = true, true
		if err := loop.processOperatorRestart(t.Context(), intent); err != nil {
			t.Fatal(err)
		}
		row, err := store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded {
			t.Fatalf("outcome = %+v, %v", row, err)
		}
		if vmm.destroys != 1 || vmm.rpcs != 1 || store.saves != 2 {
			t.Fatalf("effects=%d RPCs=%d saves=%d", vmm.destroys, vmm.rpcs, store.saves)
		}
		if loop.engine.ledger.ResidentFor(instance.ID) {
			t.Fatal("confirmed teardown retained admission")
		}
	})
	t.Run("crash_after_vmmd_receipt_resumes_database_work_only", func(t *testing.T) {
		loop, store, vmm, intent, _ := restartLoopFixture(t)
		store.failures = api.OperatorRestartPersistenceAttempts
		if err := loop.processOperatorRestart(t.Context(), intent); err == nil {
			t.Fatal("expected persistence failure")
		}
		row, err := store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
		if err != nil || row.Status != state.OperatorIntentRunning || row.RestartExecution.DestroyedAt == nil {
			t.Fatalf("lost recovery evidence: %+v, %v", row, err)
		}
		if _, err := store.MemStore.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		// A new scheduler object has no execution history in memory.
		replacement := &Loop{engine: newEngine(t, loop.engine.Store(), vmm, &fakeNotifier{}, "1.10.0"), log: testLog()}
		replacement.drainPendingOperatorIntents(t.Context())
		row, err = store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || vmm.destroys != 1 || vmm.rpcs != 1 {
			t.Fatalf("recovery = %+v, %v, effects=%d RPCs=%d", row, err, vmm.destroys, vmm.rpcs)
		}
	})
	t.Run("interrupted_teardown_is_unknown_and_late_receipt_recovers", func(t *testing.T) {
		loop, store, vmm, intent, instance := restartLoopFixture(t)
		vmm.interrupt = true
		if err := loop.processOperatorRestart(t.Context(), intent); err != nil {
			t.Fatal(err)
		}
		row, err := store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
		if err != nil || row.Status != state.OperatorIntentOutcomeUnknown {
			t.Fatalf("outcome = %+v, %v", row, err)
		}
		if !loop.engine.ledger.ResidentFor(instance.ID) {
			t.Fatal("uncertain teardown released admission")
		}
		loop.drainPendingOperatorIntents(t.Context())
		if vmm.destroys != 1 {
			t.Fatal("uncertain action redispatched")
		}
		e, err := store.MemStore.GetOperatorRestart(t.Context(), intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MemStore.AcknowledgeOperatorRestartDestroy(t.Context(), intent.ID, e.DestroyToken); err != nil {
			t.Fatal(err)
		}
		loop.drainPendingOperatorIntents(t.Context())
		row, err = store.MemStore.GetOperatorIntent(t.Context(), intent.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || vmm.destroys != 1 {
			t.Fatalf("late acknowledgement = %+v, %v", row, err)
		}
	})
	t.Run("stale_worker_is_rejected_before_dispatch", func(t *testing.T) {
		loop, store, vmm, intent, _ := restartLoopFixture(t)
		if _, err := store.MemStore.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MemStore.ClaimPendingOperatorIntent(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := loop.processOperatorRestart(t.Context(), intent); !errors.Is(err, state.ErrOperatorRestartClaimLost) {
			t.Fatalf("stale worker = %v", err)
		}
		if vmm.rpcs != 0 || vmm.destroys != 0 {
			t.Fatal("stale worker reached vmmd")
		}
	})
}

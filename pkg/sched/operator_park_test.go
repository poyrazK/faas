package sched

// adr: 1525

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

type parkPersistenceStore struct {
	MemStore        *state.MemStore
	failures, saves int
	loseAck         bool
}

func (s *parkPersistenceStore) CompleteOperatorPark(ctx context.Context, c state.OperatorParkCompletion) (state.OperatorParkResult, error) {
	s.saves++
	if s.failures > 0 {
		s.failures--
		return state.OperatorParkResult{}, errors.New("database unavailable")
	}
	r, err := s.MemStore.CompleteOperatorPark(ctx, c)
	if err == nil && s.loseAck {
		s.loseAck = false
		return state.OperatorParkResult{}, errors.New("commit acknowledgement lost")
	}
	return r, err
}

type parkProtocolVMM struct {
	*fakeVMM
	store                              state.OperatorParkStore
	rpcs, effects                      int
	interrupt, loseReply, stopFallback bool
	beforeGeneration                   func()
}

func (v *parkProtocolVMM) OperatorRestartGeneration(context.Context, string, string) (string, error) {
	if v.beforeGeneration != nil {
		v.beforeGeneration()
	}
	return "physical-generation", nil
}
func (v *parkProtocolVMM) ExecuteOperatorPark(ctx context.Context, node, id, token string) error {
	v.rpcs++
	e, execute, err := v.store.StartOperatorPark(ctx, id, token, node)
	if err != nil {
		return err
	}
	if execute {
		v.effects++
		if v.interrupt {
			return errors.New("vmmd interrupted")
		}
		r := state.OperatorParkReceipt{Outcome: string(state.StateParked), MemBytes: 1024, VMStateBytes: 64, StoredBytes: 1088}
		if v.stopFallback || e.Target.Action == state.OperatorParkStop {
			r = state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: "capture unavailable; cleanup verified"}
		}
		if err := v.store.AcknowledgeOperatorPark(ctx, id, e.EffectToken, r); err != nil {
			return err
		}
	}
	if v.loseReply {
		return errors.New("wire acknowledgement lost")
	}
	return nil
}

func parkLoopFixture(t *testing.T) (*Loop, *parkPersistenceStore, *parkProtocolVMM, state.OperatorIntent, state.Instance) {
	t.Helper()
	fakeStore := &parkPersistenceStore{MemStore: state.NewMemStore()}
	store := statetest.NewMem(fakeStore)
	account, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	vmm := &parkProtocolVMM{fakeVMM: &fakeVMM{}, store: store}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	engine.Ledger().Admit(Request{Instance: ins.ID, AppID: app.ID, Plan: api.PlanPro, RAMMB: 512, VCPU: 2, MaxConcurrency: 5})
	_, err = store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForcePark, ins.ID, &account.ID, account.ID, "park", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	i, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return &Loop{engine: engine, log: testLog()}, fakeStore, vmm, i, ins
}

func TestOperatorParkSchedulerRecovery(t *testing.T) {
	t.Run("fresh_traffic_is_cancelled_not_successful", func(t *testing.T) {
		l, s, v, i, ins := parkLoopFixture(t)
		v.beforeGeneration = func() {
			if _, err := s.MemStore.IncInstanceRequestCount(t.Context(), ins.ID, 1); err != nil {
				t.Fatal(err)
			}
		}
		l.processOperatorIntent(t.Context(), i)
		row, err := s.MemStore.GetOperatorIntent(t.Context(), i.ID)
		if err != nil || row.Status != state.OperatorIntentCancelled || v.effects != 0 || v.rpcs != 0 || !l.engine.ledger.ResidentFor(ins.ID) {
			t.Fatalf("cancel=%+v err=%v effects=%d rpcs=%d", row, err, v.effects, v.rpcs)
		}
		current, err := s.MemStore.InstanceByID(t.Context(), ins.ID)
		if err != nil || current.State != string(state.StateRunning) {
			t.Fatalf("cancel state=%+v %v", current, err)
		}
	})
	t.Run("lost_replies_do_not_repeat_effects", func(t *testing.T) {
		l, s, v, i, ins := parkLoopFixture(t)
		v.loseReply = true
		s.loseAck = true
		if err := l.processOperatorPark(t.Context(), i); err != nil {
			t.Fatal(err)
		}
		row, err := s.MemStore.GetOperatorIntent(t.Context(), i.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || v.effects != 1 || v.rpcs != 1 || s.saves != 2 {
			t.Fatalf("lost reply=%+v %v effects=%d rpcs=%d saves=%d", row, err, v.effects, v.rpcs, s.saves)
		}
		if l.engine.ledger.ResidentFor(ins.ID) {
			t.Fatal("verified completion retained capacity")
		}
	})
	t.Run("database_failure_retains_capacity_and_resumes_database_only", func(t *testing.T) {
		l, s, v, i, ins := parkLoopFixture(t)
		s.failures = api.OperatorRestartPersistenceAttempts
		if err := l.processOperatorPark(t.Context(), i); err == nil {
			t.Fatal("database failure returned success")
		}
		if !l.engine.ledger.ResidentFor(ins.ID) {
			t.Fatal("failed commit released capacity")
		}
		row, err := s.MemStore.GetOperatorIntent(t.Context(), i.ID)
		if err != nil || row.ParkExecution.Receipt == nil || row.Status != state.OperatorIntentRunning {
			t.Fatalf("lost receipt=%+v %v", row, err)
		}
		if _, err := s.MemStore.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		replacement := &Loop{engine: newEngine(t, l.engine.Store(), v, &fakeNotifier{}, "1.10.0"), log: testLog()}
		replacement.drainPendingOperatorIntents(t.Context())
		row, err = s.MemStore.GetOperatorIntent(t.Context(), i.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || v.effects != 1 || v.rpcs != 1 {
			t.Fatalf("recovery=%+v %v effects=%d rpcs=%d", row, err, v.effects, v.rpcs)
		}
	})
	t.Run("uncertain_operation_never_redispatches_and_late_receipt_recovers", func(t *testing.T) {
		l, s, v, i, ins := parkLoopFixture(t)
		v.interrupt = true
		if err := l.processOperatorPark(t.Context(), i); err != nil {
			t.Fatal(err)
		}
		row, err := s.MemStore.GetOperatorIntent(t.Context(), i.ID)
		if err != nil || row.Status != state.OperatorIntentOutcomeUnknown || !l.engine.ledger.ResidentFor(ins.ID) {
			t.Fatalf("unknown=%+v %v", row, err)
		}
		l.drainPendingOperatorIntents(t.Context())
		if v.rpcs != 1 {
			t.Fatal("unknown operation redispatched")
		}
		e, err := s.MemStore.GetOperatorPark(t.Context(), i.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MemStore.AcknowledgeOperatorPark(t.Context(), i.ID, e.EffectToken, state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: "capture failed, cleanup verified"}); err != nil {
			t.Fatal(err)
		}
		l.drainPendingOperatorIntents(t.Context())
		row, err = s.MemStore.GetOperatorIntent(t.Context(), i.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || row.ParkExecution.TerminalState != string(state.StateStopped) || v.effects != 1 || v.rpcs != 1 {
			t.Fatalf("late receipt=%+v %v", row, err)
		}
	})
	t.Run("stale_claim_is_rejected_before_generation_lookup", func(t *testing.T) {
		l, s, v, i, _ := parkLoopFixture(t)
		if _, err := s.MemStore.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MemStore.ClaimPendingOperatorIntent(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := l.processOperatorPark(t.Context(), i); !errors.Is(err, state.ErrOperatorParkClaimLost) || v.effects != 0 {
			t.Fatalf("stale claim=%v effects=%d", err, v.effects)
		}
	})
}

func TestOperatorParkWatchdogRetainsUncertainOwnership(t *testing.T) {
	l, s, v, i, ins := parkLoopFixture(t)
	v.interrupt = true
	if err := l.processOperatorPark(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	watchdog := NewWatchdog(l.engine.Store(), l.engine, testLog())
	watchdog.runOne(t.Context(), time.Now().Add(time.Hour), state.StateSnapshotting, SnapshotSweepBudget, StuckSnapshotTimeout)
	row, err := s.MemStore.InstanceByID(t.Context(), ins.ID)
	if err != nil || row.State != string(state.StateSnapshotting) || v.destroys != 0 || !l.engine.ledger.ResidentFor(ins.ID) {
		t.Fatalf("watchdog bypassed journal: %+v %v destroys=%d", row, err, v.destroys)
	}
}

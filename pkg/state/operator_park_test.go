package state_test

// adr: 1525

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostport"
	"github.com/onebox-faas/faas/pkg/state"
)

type parkTestStore interface {
	state.Store
	state.OperatorParkStore
	state.AuditEventOutboxStore
	state.HostPortLeaseStore
}

type parkFixture struct {
	store  parkTestStore
	intent state.OperatorIntent
	target state.OperatorParkTarget
}

func seedPark(t *testing.T, s parkTestStore) parkFixture {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@park.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "park-" + uuid.NewString(), RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:park"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireHostPortLeases(ctx, node.ID, ins.ID, []hostport.Request{{Name: "http", Protocol: hostport.TCP, GuestPort: 8080}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertOperatorIntent(ctx, state.OperatorIntentKindForcePark, ins.ID, &acct.ID, acct.ID, "recover park", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, uuid.NewString())
	target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: ins.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, WakeID: ins.WakeID, Generation: uuid.NewString()}, InitialState: ins.State, Mode: ins.Mode, Action: state.OperatorParkCapture, StartedAt: ins.StartedAt, LastRequestAt: ins.LastRequestAt, RequestCount: ins.RequestCount, MemoryKey: key, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), FCVersion: "1.10.0"}
	return parkFixture{s, i, target}
}

func (f parkFixture) prepare(t *testing.T) state.OperatorParkExecution {
	t.Helper()
	e, err := f.store.PrepareOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func (f parkFixture) start(t *testing.T) state.OperatorParkExecution {
	t.Helper()
	e, execute, err := f.store.StartOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID)
	if err != nil || !execute {
		t.Fatalf("start=%+v execute=%v err=%v", e, execute, err)
	}
	return e
}
func (f parkFixture) complete(status state.OperatorIntentStatus, reason string) state.OperatorParkCompletion {
	return state.OperatorParkCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: status, Error: reason}
}
func capturedParkReceipt() state.OperatorParkReceipt {
	return state.OperatorParkReceipt{Outcome: string(state.StateParked), MemBytes: 1024, VMStateBytes: 64, StoredBytes: 1088}
}

func runParkStoreTests(t *testing.T, create func(*testing.T) parkTestStore) {
	t.Helper()
	t.Run("verified_completion_and_receipt_replay", func(t *testing.T) {
		f := seedPark(t, create(t))
		e := f.prepare(t)
		ins, err := f.store.InstanceByID(t.Context(), f.target.InstanceID)
		if err != nil || ins.State != string(state.StateSnapshotting) || ins.ParkedAt.IsZero() {
			t.Fatalf("prepared state=%+v err=%v", ins, err)
		}
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unproven completion=%v", err)
		}
		changed := f.target
		changed.Generation = "replacement"
		again, err := f.store.PrepareOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, changed)
		if err != nil || again.Target.Generation != e.Target.Generation {
			t.Fatalf("plan changed: %+v %v", again, err)
		}
		e = f.start(t)
		receipt := capturedParkReceipt()
		if err := f.store.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, receipt); err != nil {
			t.Fatal(err)
		}
		if err := f.store.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, receipt); err != nil {
			t.Fatal(err)
		}
		different := receipt
		different.MemBytes++
		if err := f.store.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, different); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed evidence accepted: %v", err)
		}
		result, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, ""))
		if err != nil || result.AuditOutboxID == 0 || result.Replayed {
			t.Fatalf("complete=%+v %v", result, err)
		}
		row, err := f.store.GetOperatorIntent(t.Context(), f.intent.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || row.ParkExecution.TerminalState != string(state.StateParked) || row.ParkExecution.StoppedAt == nil {
			t.Fatalf("outcome=%+v %v", row, err)
		}
		ins, err = f.store.InstanceByID(t.Context(), f.target.InstanceID)
		if err != nil || ins.State != string(state.StateParked) {
			t.Fatalf("state=%+v %v", ins, err)
		}
		ports, err := f.store.ListHostPortLeases(t.Context(), f.target.NodeID, f.target.InstanceID)
		if err != nil || len(ports) != 0 {
			t.Fatalf("ports=%+v %v", ports, err)
		}
		if _, err := f.store.DeliverAuditEvent(t.Context(), result.AuditOutboxID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PruneAuditEventOutbox(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		replay, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, ""))
		if err != nil || !replay.Replayed || replay.AuditOutboxID != result.AuditOutboxID {
			t.Fatalf("replay=%+v %v", replay, err)
		}
	})
	t.Run("fresh_traffic_cancels_before_preparation", func(t *testing.T) {
		f := seedPark(t, create(t))
		if _, err := f.store.IncInstanceRequestCount(t.Context(), f.target.InstanceID, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PrepareOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target); !errors.Is(err, state.ErrOperatorParkCancelled) {
			t.Fatalf("fresh traffic=%v", err)
		}
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentCancelled, "fresh traffic")); err != nil {
			t.Fatal(err)
		}
		ins, err := f.store.InstanceByID(t.Context(), f.target.InstanceID)
		if err != nil || ins.State != string(state.StateRunning) {
			t.Fatalf("cancel touched state: %+v %v", ins, err)
		}
		if _, err := f.store.GetOperatorPark(t.Context(), f.intent.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cancel created execution")
		}
	})
	t.Run("single_effect_stale_claim_and_late_receipt", func(t *testing.T) {
		f := seedPark(t, create(t))
		f.prepare(t)
		var effects atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, execute, err := f.store.StartOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID)
				if err != nil {
					t.Error(err)
				}
				if execute {
					effects.Add(1)
				}
			})
		}
		wg.Wait()
		if effects.Load() != 1 {
			t.Fatalf("effects=%d", effects.Load())
		}
		e, err := f.store.GetOperatorPark(t.Context(), f.intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentOutcomeUnknown, "receipt missing")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
			t.Fatalf("unknown redispatched: %v", err)
		}
		if err := f.store.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, state.OperatorParkReceipt{Outcome: string(state.StateStopped), Reason: "capture failed; cleanup verified"}); err != nil {
			t.Fatal(err)
		}
		next, err := f.store.ClaimPendingOperatorIntent(t.Context())
		if err != nil || next.ClaimToken == f.intent.ClaimToken {
			t.Fatalf("late claim=%+v %v", next, err)
		}
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); !errors.Is(err, state.ErrOperatorParkClaimLost) {
			t.Fatalf("old completion=%v", err)
		}
		f.intent = next
		if _, execute, err := f.store.StartOperatorPark(t.Context(), next.ID, next.ClaimToken, f.target.NodeID); err != nil || execute {
			t.Fatalf("repeat=%v %v", execute, err)
		}
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); err != nil {
			t.Fatal(err)
		}
		ins, err := f.store.InstanceByID(t.Context(), f.target.InstanceID)
		if err != nil || ins.State != string(state.StateStopped) {
			t.Fatalf("fallback=%+v %v", ins, err)
		}
	})
	t.Run("claim_reclaim_does_not_repeat_effect", func(t *testing.T) {
		f := seedPark(t, create(t))
		f.prepare(t)
		f.start(t)
		if _, err := f.store.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		next, err := f.store.ClaimPendingOperatorIntent(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.store.StartOperatorPark(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID); !errors.Is(err, state.ErrOperatorParkClaimLost) {
			t.Fatalf("old dispatch=%v", err)
		}
		if _, execute, err := f.store.StartOperatorPark(t.Context(), next.ID, next.ClaimToken, f.target.NodeID); err != nil || execute {
			t.Fatalf("new dispatch=%v %v", execute, err)
		}
	})
	t.Run("legacy_setters_and_missing_receipts_cannot_succeed", func(t *testing.T) {
		f := seedPark(t, create(t))
		if err := f.store.MarkOperatorIntentSucceeded(t.Context(), f.intent.ID, nil); err == nil {
			t.Fatal("legacy success accepted")
		}
		if err := f.store.MarkOperatorIntentFailed(t.Context(), f.intent.ID, "oops", nil); err == nil {
			t.Fatal("legacy failure accepted")
		}
		f.prepare(t)
		f.start(t)
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentFailed, "failed")); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("failure erased uncertainty: %v", err)
		}
	})
	t.Run("stop_action_keeps_legal_state_edges", func(t *testing.T) {
		f := seedPark(t, create(t))
		f.target.Action = state.OperatorParkStop
		f.prepare(t)
		e := f.start(t)
		if err := f.store.AcknowledgeOperatorPark(t.Context(), f.intent.ID, e.EffectToken, state.OperatorParkReceipt{Outcome: string(state.StateStopped)}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.CompleteOperatorPark(t.Context(), f.complete(state.OperatorIntentSucceeded, "")); err != nil {
			t.Fatal(err)
		}
		row, err := f.store.GetOperatorIntent(t.Context(), f.intent.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(row.ParkExecution)
		if !json.Valid(encoded) {
			t.Fatal("invalid journal")
		}
	})
}

func TestOperatorParkMemory(t *testing.T) {
	runParkStoreTests(t, func(*testing.T) parkTestStore { return state.NewMemStore() })
}

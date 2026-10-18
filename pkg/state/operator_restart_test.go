package state_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostport"
	"github.com/onebox-faas/faas/pkg/state"
)

type restartTestStore = contractTestStore

type restartFixture struct {
	store     restartTestStore
	intent    state.OperatorIntent
	target    state.OperatorRestartTarget
	snapshots []string
}

func seedRestart(t *testing.T, store restartTestStore) restartFixture {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@restart.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "restart-" + uuid.NewString(), RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:restart"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireHostPortLeases(ctx, node.ID, instance.ID, []hostport.Request{{Name: "http", Protocol: hostport.TCP, GuestPort: 8080}}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snapshot, createErr := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.10.0", StorageKey: uuid.NewString(), Tier: tier, MemBytes: 1024, DiskBytes: 1024})
		if createErr != nil {
			t.Fatal(createErr)
		}
		ids = append(ids, snapshot.ID)
	}
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	id, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindForceRestart, instance.ID, &account.ID, account.ID, "recover wedged guest", nil, &traceID)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil || intent.ID != id {
		t.Fatalf("claim = %+v, %v", intent, err)
	}
	return restartFixture{store: store, intent: intent, snapshots: ids, target: state.OperatorRestartTarget{
		InstanceID: instance.ID, AppID: app.ID, DeploymentID: deployment.ID, NodeID: instance.NodeID, WakeID: instance.WakeID, Generation: uuid.NewString(),
	}}
}

func (f restartFixture) prepare(t *testing.T) state.OperatorRestartExecution {
	t.Helper()
	e, err := f.store.PrepareOperatorRestart(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (f restartFixture) acknowledge(t *testing.T) state.OperatorRestartExecution {
	t.Helper()
	e, execute, err := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID)
	if err != nil || !execute {
		t.Fatalf("start = %+v, %v, %v", e, execute, err)
	}
	if err := f.store.AcknowledgeOperatorRestartDestroy(t.Context(), f.intent.ID, e.DestroyToken); err != nil {
		t.Fatal(err)
	}
	return e
}

func runRestartStoreTests(t *testing.T, create func(*testing.T) restartTestStore) {
	t.Helper()
	t.Run("receipt_and_original_snapshot_plan_survive_reentry", func(t *testing.T) {
		f := seedRestart(t, create(t))
		e := f.prepare(t)
		if !slices.Equal(e.Target.SnapshotIDs, f.snapshots) {
			t.Fatalf("snapshots = %v, want %v", e.Target.SnapshotIDs, f.snapshots)
		}
		for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
			if _, err := f.store.LatestSnapshotForTier(t.Context(), f.target.DeploymentID, tier); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("snapshot remains usable: %v", err)
			}
		}
		later, err := f.store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: f.target.DeploymentID, FCVersion: "1.10.0", StorageKey: "later", Tier: state.SnapshotTierWarm, MemBytes: 1024, DiskBytes: 1024})
		if err != nil {
			t.Fatal(err)
		}
		changed := f.target
		changed.Generation = "replacement"
		replayed, err := f.store.PrepareOperatorRestart(t.Context(), f.intent.ID, f.intent.ClaimToken, changed)
		if err != nil || replayed.Target.Generation != e.Target.Generation || !slices.Equal(replayed.Target.SnapshotIDs, f.snapshots) {
			t.Fatalf("plan changed: %+v, %v", replayed, err)
		}
		fresh, err := f.store.LatestSnapshotForTier(t.Context(), f.target.DeploymentID, state.SnapshotTierWarm)
		if err != nil || fresh.ID != later.ID {
			t.Fatalf("retry invalidated a later snapshot: %+v, %v", fresh, err)
		}
		completion := state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentSucceeded}
		if _, err := f.store.CompleteOperatorRestart(t.Context(), completion); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("success without receipt: %v", err)
		}
		f.acknowledge(t)
		result, err := f.store.CompleteOperatorRestart(t.Context(), completion)
		if err != nil || result.Replayed || result.AuditOutboxID == 0 {
			t.Fatalf("complete = %+v, %v", result, err)
		}
		row, err := f.store.GetOperatorIntent(t.Context(), f.intent.ID)
		if err != nil || row.Status != state.OperatorIntentSucceeded || row.RestartExecution.Phase != state.RestartCompleted || !slices.Equal(row.SnapIDsMarkedStale, f.snapshots) {
			t.Fatalf("outcome = %+v, %v", row, err)
		}
		instance, err := f.store.InstanceByID(t.Context(), f.target.InstanceID)
		if err != nil || instance.State != string(state.StateStopped) {
			t.Fatalf("instance = %+v, %v", instance, err)
		}
		if instance.LastRequestAt.Before(*row.RestartExecution.DestroyedAt) {
			t.Fatal("restart did not refresh the original instance idle budget")
		}
		ports, err := f.store.ListHostPortLeases(t.Context(), f.target.NodeID, f.target.InstanceID)
		if err != nil || len(ports) != 0 {
			t.Fatalf("completed restart retained ports: %v, %v", ports, err)
		}
		if _, err := f.store.AcquireHostPortLeases(t.Context(), f.target.NodeID, f.target.InstanceID, []hostport.Request{{Name: "replacement", Protocol: hostport.TCP, GuestPort: 9090}}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DeliverAuditEvent(t.Context(), result.AuditOutboxID); err != nil {
			t.Fatal(err)
		}
		events, err := f.store.ListEvents(t.Context(), *f.intent.AccountID, 10)
		if err != nil || len(events) != 1 {
			t.Fatalf("audit = %+v, %v", events, err)
		}
		var audit map[string]any
		if err := json.Unmarshal(events[0].Data, &audit); err != nil {
			t.Fatal(err)
		}
		if audit["trace_id"] != *f.intent.TraceID || audit["result"] != "succeeded" {
			t.Fatalf("audit attribution = %v", audit)
		}
		correlated, err := f.store.ListEventsByTraceID(t.Context(), *f.intent.TraceID, 10)
		if err != nil || len(correlated) != 1 {
			t.Fatalf("outcome missing from indexed trace reads: %+v, %v", correlated, err)
		}
		if _, err := f.store.PruneAuditEventOutbox(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		duplicate, err := f.store.CompleteOperatorRestart(t.Context(), completion)
		if err != nil || !duplicate.Replayed || duplicate.AuditOutboxID != result.AuditOutboxID {
			t.Fatalf("lost acknowledgement replay = %+v, %v", duplicate, err)
		}
		ports, err = f.store.ListHostPortLeases(t.Context(), f.target.NodeID, f.target.InstanceID)
		if err != nil || len(ports) != 1 || ports[0].ListenerName != "replacement" {
			t.Fatalf("completion replay touched replacement ports: %v, %v", ports, err)
		}
		completion.Status, completion.Error = state.OperatorIntentFailed, "changed result"
		if _, err := f.store.CompleteOperatorRestart(t.Context(), completion); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed completion accepted: %v", err)
		}
	})
	t.Run("new_claim_fences_old_workers_and_only_one_vmmd_starts", func(t *testing.T) {
		f := seedRestart(t, create(t))
		f.prepare(t)
		if _, err := f.store.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		newClaim, err := f.store.ClaimPendingOperatorIntent(t.Context())
		if err != nil || newClaim.ClaimToken == f.intent.ClaimToken {
			t.Fatalf("new claim = %+v, %v", newClaim, err)
		}
		if _, _, err := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID); !errors.Is(err, state.ErrOperatorRestartClaimLost) {
			t.Fatalf("stale start = %v", err)
		}
		if _, err := f.store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentFailed, Error: "stale"}); !errors.Is(err, state.ErrOperatorRestartClaimLost) {
			t.Fatalf("stale complete = %v", err)
		}
		var starts atomic.Int32
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() {
				_, execute, startErr := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, newClaim.ClaimToken, f.target.NodeID)
				if startErr != nil {
					t.Error(startErr)
				}
				if execute {
					starts.Add(1)
				}
			})
		}
		wg.Wait()
		if starts.Load() != 1 {
			t.Fatalf("physical execution owners = %d", starts.Load())
		}
	})
	t.Run("unknown_blocks_redispatch_and_late_receipt_reconciles", func(t *testing.T) {
		f := seedRestart(t, create(t))
		f.prepare(t)
		e, _, err := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentOutcomeUnknown, Error: "teardown acknowledgement lost"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.ClaimPendingOperatorIntent(t.Context()); !errors.Is(err, state.ErrOperatorIntentNotFound) {
			t.Fatalf("uncertain execution redispatched: %v", err)
		}
		counts, err := f.store.OperatorIntentOutcomeMissingCounts(t.Context(), time.Now().Add(-time.Hour))
		if err != nil || counts["force_restart"] != 1 {
			t.Fatalf("uncertain outcome disappeared from health: %v, %v", counts, err)
		}
		if err := f.store.AcknowledgeOperatorRestartDestroy(t.Context(), f.intent.ID, e.DestroyToken); err != nil {
			t.Fatal(err)
		}
		claim, err := f.store.ClaimPendingOperatorIntent(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, execute, err := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, claim.ClaimToken, f.target.NodeID)
		if err != nil || execute {
			t.Fatalf("recovery attempted another destroy: %v, %v", execute, err)
		}
		_, err = f.store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: claim.ClaimToken, Status: state.OperatorIntentSucceeded})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("wrong_node_and_changed_lifecycle_never_receive_the_action", func(t *testing.T) {
		f := seedRestart(t, create(t))
		f.prepare(t)
		if _, _, err := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, f.intent.ClaimToken, uuid.NewString()); !errors.Is(err, state.ErrOperatorRestartTargetChanged) {
			t.Fatalf("wrong node accepted: %v", err)
		}
		if err := f.store.UpdateInstanceState(t.Context(), f.target.InstanceID, string(state.StateParked)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.store.StartOperatorRestartDestroy(t.Context(), f.intent.ID, f.intent.ClaimToken, f.target.NodeID); !errors.Is(err, state.ErrOperatorRestartTargetChanged) {
			t.Fatalf("new wake accepted: %v", err)
		}
	})
	t.Run("bounded_failure_and_legacy_setters_cannot_bypass_restart", func(t *testing.T) {
		f := seedRestart(t, create(t))
		if err := f.store.MarkOperatorIntentSucceeded(t.Context(), f.intent.ID, nil); err == nil {
			t.Fatal("unfenced success accepted")
		}
		if err := f.store.MarkOperatorIntentFailed(t.Context(), f.intent.ID, "unfenced", nil); err == nil {
			t.Fatal("unfenced failure accepted")
		}
		_, err := f.store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: f.intent.ID, ClaimToken: f.intent.ClaimToken, Status: state.OperatorIntentFailed, Error: strings.Repeat("x", 4000)})
		if err != nil {
			t.Fatal(err)
		}
		row, err := f.store.GetOperatorIntent(t.Context(), f.intent.ID)
		if err != nil || len(row.Error) > api.AuditReasonMaxBytes {
			t.Fatalf("unbounded error: %d, %v", len(row.Error), err)
		}
	})
}

func TestOperatorRestartMemory(t *testing.T) {
	runRestartStoreTests(t, func(*testing.T) restartTestStore { return state.NewMemStore() })
}

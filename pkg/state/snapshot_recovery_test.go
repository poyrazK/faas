// adr: 1523
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type coldBootTestStore interface {
	state.Store
	state.SnapshotRecoveryStore
	state.OperatorColdBootStore
	state.AuditEventOutboxStore
}

type coldBootFixture struct {
	store     coldBootTestStore
	account   state.Account
	app       state.App
	dep       state.Deployment
	intent    state.OperatorIntent
	snapshots []string
	nodeID    string
}

func seedColdBoot(t *testing.T, store coldBootTestStore) coldBootFixture {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "cold-boot-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "cold-boot-" + uuid.NewString()[:8], Type: state.AppTypeFunction, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:cold-boot", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	f := coldBootFixture{store: store, account: account, app: app, dep: dep, nodeID: node.ID}
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snap, err := store.CreateSnapshot(ctx, f.snapshot(tier))
		if err != nil {
			t.Fatal(err)
		}
		f.snapshots = append(f.snapshots, snap.ID)
	}
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	id, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindForceColdBoot, dep.ID, &account.ID, account.ID, "recover suspect cache", json.RawMessage(`{"source":"test"}`), &traceID)
	if err != nil {
		t.Fatal(err)
	}
	f.intent, err = store.ClaimPendingOperatorIntent(ctx)
	if err != nil || f.intent.ID != id {
		t.Fatalf("claim: %+v, %v", f.intent, err)
	}
	return f
}

func (f coldBootFixture) snapshot(tier string) state.Snapshot {
	return state.Snapshot{DeploymentID: f.dep.ID, FCVersion: "1.10.0", Tier: tier,
		StorageKey: state.SnapshotCaptureMemKey(f.dep.ID, tier, uuid.NewString()), MemBytes: 512 << 20}
}

func (f coldBootFixture) source(t *testing.T, snapshotID string) state.Instance {
	t.Helper()
	initial := string(state.StateColdBooting)
	if snapshotID != "" {
		initial = string(state.StateWaking)
	}
	ins, err := f.store.CreateInstance(t.Context(), f.app.ID, f.dep.ID, initial, 512, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.BindInstanceSnapshotRecovery(t.Context(), ins.ID, ins.WakeID, snapshotID); err != nil {
		t.Fatal(err)
	}
	ins, err = f.store.PublishInstanceRuntime(t.Context(), ins.ID, initial, "cold-boot-netns-"+ins.ID, "192.0.2.9", 20001)
	if err != nil {
		t.Fatal(err)
	}
	return ins
}

func (f coldBootFixture) execute(t *testing.T) state.OperatorColdBootResult {
	t.Helper()
	r, err := f.store.ExecuteOperatorColdBoot(t.Context(), f.intent.ID, f.intent.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func runColdBootStoreTests(t *testing.T, create func(*testing.T) coldBootTestStore) {
	t.Helper()
	t.Run("atomic_receipt_replay_preserves_new_cache_after_prune", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		r := f.execute(t)
		if r.Status != state.OperatorIntentSucceeded || r.Generation != 1 || r.AuditOutboxID <= 0 || r.Replayed || !slices.Equal(r.SnapshotIDs, f.snapshots) {
			t.Fatalf("completion: %+v", r)
		}
		row, err := f.store.GetOperatorIntent(t.Context(), f.intent.ID)
		if err != nil || row.Status != r.Status || row.ColdBootGeneration != r.Generation || row.FinishedAt == nil || !slices.Equal(row.SnapIDsMarkedStale, f.snapshots) {
			t.Fatalf("polling result: %+v, %v", row, err)
		}
		source := f.source(t, "")
		fresh, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), f.snapshot(state.SnapshotTierWarm), source.ID, source.StartedAt)
		if err != nil || fresh.RecoveryGeneration != 1 {
			t.Fatalf("fresh publication: %+v, %v", fresh, err)
		}
		for attempt := range 2 {
			replay := f.execute(t)
			if !replay.Replayed || replay.AuditOutboxID != r.AuditOutboxID || replay.Generation != r.Generation || !slices.Equal(replay.SnapshotIDs, r.SnapshotIDs) {
				t.Fatalf("replay changed result: %+v", replay)
			}
			latest, err := f.store.LatestSnapshotForTier(t.Context(), f.dep.ID, state.SnapshotTierWarm)
			if err != nil || latest.ID != fresh.ID {
				t.Fatalf("replay invalidated new cache: %+v, %v", latest, err)
			}
			if attempt == 0 {
				if _, err := f.store.DeliverAuditEvent(t.Context(), r.AuditOutboxID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.PruneAuditEventOutbox(t.Context(), time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
		}
		events, err := f.store.ListEvents(t.Context(), f.account.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, event := range events {
			if event.Kind != "operator.action.force_cold_boot.outcome" {
				continue
			}
			found++
			var payload struct {
				Generation int64     `json:"recovery_generation"`
				FinishedAt time.Time `json:"finished_at"`
				IDs        []string  `json:"snap_ids_marked_stale"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Generation != 1 || !payload.FinishedAt.Equal(*row.FinishedAt) || !slices.Equal(payload.IDs, f.snapshots) {
				t.Fatalf("audit disagrees with receipt: %s", event.Data)
			}
		}
		if found != 1 {
			t.Fatalf("outcome events = %d, want 1", found)
		}
		correlated, err := f.store.ListEventsByTraceID(t.Context(), *f.intent.TraceID, 10)
		if err != nil || len(correlated) != 1 {
			t.Fatalf("indexed trace outcome: %+v, %v", correlated, err)
		}
	})
	t.Run("old_and_inflight_processes_cannot_repopulate_either_tier", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		old := f.source(t, f.snapshots[0])
		f.execute(t)
		// Readiness/started_at after the action must not relabel an older boot.
		if err := f.store.SetInstanceRuntime(t.Context(), old.ID, "late-readiness", "192.0.2.9", 20001); err != nil {
			t.Fatal(err)
		}
		old, err := f.store.InstanceByID(t.Context(), old.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
			candidate := f.snapshot(tier)
			candidate.RecoveryGeneration = 1 // Payload metadata cannot grant authority.
			if _, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), candidate, old.ID, old.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
				t.Fatalf("late %s publication: %v", tier, err)
			}
			if _, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), candidate, "", time.Time{}); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
				t.Fatalf("source-less %s publication: %v", tier, err)
			}
			candidate.RecoveryGeneration = 0
			if _, err := f.store.CreateSnapshot(t.Context(), candidate); err == nil {
				t.Fatal("legacy direct writer bypassed recovery")
			}
			candidate.RecoveryGeneration = 1
			if _, err := f.store.CreateSnapshot(t.Context(), candidate); err == nil {
				t.Fatal("direct writer forged recovery lineage")
			}
			if _, err := f.store.LatestSnapshotForTier(t.Context(), f.dep.ID, tier); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("%s cache remained usable: %v", tier, err)
			}
		}
		// A snapshot chosen before the boundary cannot be admitted afterward.
		ins, err := f.store.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(state.StateWaking), 512, f.nodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.BindInstanceSnapshotRecovery(t.Context(), ins.ID, ins.WakeID, f.snapshots[0]); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old selection admitted: %v", err)
		}
	})
	t.Run("fresh_cold_boot_then_restore_inherit_generation", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		f.execute(t)
		cold := f.source(t, "")
		init, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), f.snapshot(state.SnapshotTierInit), cold.ID, cold.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		restored := f.source(t, init.ID)
		warm, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), f.snapshot(state.SnapshotTierWarm), restored.ID, restored.StartedAt)
		if err != nil || warm.RecoveryGeneration != 1 {
			t.Fatalf("restore inheritance: %+v, %v", warm, err)
		}
		latest, err := f.store.LatestSnapshot(t.Context(), f.dep.ID)
		if err != nil || latest.ID != warm.ID {
			t.Fatalf("latest snapshot: %+v, %v", latest, err)
		}
		second, err := f.store.InvalidateSnapshotRecovery(t.Context(), f.dep.ID)
		if err != nil || second.Generation != 2 {
			t.Fatalf("second recovery: %+v, %v", second, err)
		}
		if fresh, err := f.store.InstanceSnapshotRecoveryFresh(t.Context(), restored.ID); err != nil || fresh {
			t.Fatalf("old source freshness = %v, %v", fresh, err)
		}
		if _, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), f.snapshot(state.SnapshotTierInit), restored.ID, restored.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("earlier recovery source republished: %v", err)
		}
		if replay := f.execute(t); replay.Generation != 1 || !replay.Replayed {
			t.Fatalf("old command changed the newer boundary: %+v", replay)
		}
	})
	t.Run("inflight_boot_cannot_rebind_after_boundary", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		ins, err := f.store.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(state.StateColdBooting), 512, f.nodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.BindInstanceSnapshotRecovery(t.Context(), ins.ID, ins.WakeID, ""); err != nil {
			t.Fatal(err)
		}
		f.execute(t)
		// Re-entry recovers the old binding rather than taking the latest generation.
		if err := f.store.BindInstanceSnapshotRecovery(t.Context(), ins.ID, ins.WakeID, ""); err != nil {
			t.Fatal(err)
		}
		ins, err = f.store.PublishInstanceRuntime(t.Context(), ins.ID, string(state.StateColdBooting), "late-boot", "192.0.2.9", 20001)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), f.snapshot(state.SnapshotTierInit), ins.ID, ins.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("late boot gained current lineage: %v", err)
		}
	})
	t.Run("warm_and_frozen_publication_recheck_boundary", func(t *testing.T) {
		for _, paused := range []state.State{state.StateWarm, state.StateFrozen} {
			t.Run(string(paused), func(t *testing.T) {
				f := seedColdBoot(t, create(t))
				ins, err := f.store.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(state.StateColdBooting), 512, f.nodeID, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				if err := f.store.BindInstanceSnapshotRecovery(t.Context(), ins.ID, ins.WakeID, ""); err != nil {
					t.Fatal(err)
				}
				values, err := f.store.RuntimeAppValuesForDeployment(t.Context(), f.account.ID, f.app.ID, f.dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				fence, err := state.NewRuntimeAppConfigFence(values)
				if err != nil {
					t.Fatal(err)
				}
				publication := state.RuntimeInstancePublication{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
					ExpectedState: string(state.StateColdBooting), Fence: fence.SecretFence, ConfigFence: fence, Netns: "paused-recovery", HostIP: "192.0.2.9", GuestUID: 20001}
				if _, err := f.store.PublishOwnedInstanceRuntime(t.Context(), publication); err != nil {
					t.Fatal(err)
				}
				if err := f.store.UpdateInstanceState(t.Context(), ins.ID, string(paused)); err != nil {
					t.Fatal(err)
				}
				f.execute(t)
				publication.ExpectedState = string(paused)
				if _, err := f.store.PublishOwnedInstanceRuntime(t.Context(), publication); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("stale paused process published RUNNING: %v", err)
				}
			})
		}
	})
	t.Run("mismatched_account_cannot_invalidate_healthy_cache", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		f.execute(t)
		source := f.source(t, "")
		snapshot, err := f.store.PublishSnapshotIfRuntimeFresh(t.Context(), f.snapshot(state.SnapshotTierInit), source.ID, source.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		other, err := f.store.CreateAccount(t.Context(), "other-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceColdBoot, f.dep.ID, &other.ID, other.ID, "wrong account", nil, nil); err != nil {
			t.Fatal(err)
		}
		i, err := f.store.ClaimPendingOperatorIntent(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		r, err := f.store.ExecuteOperatorColdBoot(t.Context(), i.ID, i.ClaimToken)
		if err != nil || r.Status != state.OperatorIntentFailed || r.Generation != 0 {
			t.Fatalf("mismatched account: %+v %v", r, err)
		}
		latest, err := f.store.LatestSnapshot(t.Context(), f.dep.ID)
		if err != nil || latest.ID != snapshot.ID {
			t.Fatalf("mismatched account changed cache: %+v %v", latest, err)
		}
	})
	t.Run("stale_claim_fenced_and_legacy_setters_refused", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		for _, err := range []error{
			f.store.MarkOperatorIntentSucceeded(t.Context(), f.intent.ID, f.snapshots),
			f.store.MarkOperatorIntentFailed(t.Context(), f.intent.ID, "lost", nil),
		} {
			if err == nil {
				t.Fatal("unfenced terminal setter accepted cold boot")
			}
		}
		if _, err := f.store.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		next, err := f.store.ClaimPendingOperatorIntent(t.Context())
		if err != nil || next.ClaimToken == f.intent.ClaimToken {
			t.Fatalf("reclaim: %+v, %v", next, err)
		}
		if _, err := f.store.ExecuteOperatorColdBoot(t.Context(), f.intent.ID, f.intent.ClaimToken); !errors.Is(err, state.ErrOperatorColdBootClaimLost) {
			t.Fatalf("old claim executed: %v", err)
		}
		f.intent = next
		if r := f.execute(t); r.Generation != 1 {
			t.Fatalf("stale claim changed boundary: %+v", r)
		}
	})
	t.Run("concurrent_completion_commits_one_action", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		var wg sync.WaitGroup
		results := make(chan state.OperatorColdBootResult, 6)
		errorsCh := make(chan error, 6)
		for range 6 {
			wg.Go(func() {
				r, err := f.store.ExecuteOperatorColdBoot(t.Context(), f.intent.ID, f.intent.ClaimToken)
				results <- r
				errorsCh <- err
			})
		}
		wg.Wait()
		close(results)
		close(errorsCh)
		for err := range errorsCh {
			if err != nil {
				t.Fatal(err)
			}
		}
		owners, outboxID := 0, int64(0)
		for result := range results {
			if !result.Replayed {
				owners++
			}
			if outboxID == 0 {
				outboxID = result.AuditOutboxID
			}
			if result.Generation != 1 || result.AuditOutboxID != outboxID {
				t.Fatalf("concurrent result changed: %+v", result)
			}
		}
		if owners != 1 {
			t.Fatalf("owners = %d", owners)
		}
	})
	t.Run("empty_cache_still_creates_boundary_and_durable_result", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		for _, id := range f.snapshots {
			if err := f.store.MarkSnapshotStale(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		}
		if r := f.execute(t); r.Generation != 1 || len(r.SnapshotIDs) != 0 || r.AuditOutboxID == 0 {
			t.Fatalf("empty cache: %+v", r)
		}
	})
	t.Run("missing_target_has_replayable_failure_and_audit", func(t *testing.T) {
		store := create(t)
		id, err := store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceColdBoot, "invalid-deployment", nil, uuid.NewString(), "missing target", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		i, err := store.ClaimPendingOperatorIntent(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		r, err := store.ExecuteOperatorColdBoot(t.Context(), id, i.ClaimToken)
		if err != nil || r.Status != state.OperatorIntentFailed || r.Generation != 0 || r.Error == "" || r.AuditOutboxID <= 0 {
			t.Fatalf("failed completion: %+v, %v", r, err)
		}
		replay, err := store.ExecuteOperatorColdBoot(t.Context(), id, i.ClaimToken)
		if err != nil || !replay.Replayed || replay.AuditOutboxID != r.AuditOutboxID {
			t.Fatalf("failed replay: %+v, %v", replay, err)
		}
	})
	t.Run("cancelled_execution_does_not_mutate_cache", func(t *testing.T) {
		f := seedColdBoot(t, create(t))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.store.ExecuteOperatorColdBoot(ctx, f.intent.ID, f.intent.ClaimToken); err == nil {
			t.Fatal("cancelled action executed")
		}
		snap, err := f.store.LatestSnapshotForTier(t.Context(), f.dep.ID, state.SnapshotTierWarm)
		if err != nil || snap.ID != f.snapshots[0] {
			t.Fatalf("cancelled action changed cache: %+v, %v", snap, err)
		}
	})
}

func TestColdBootRecoveryMemory(t *testing.T) {
	runColdBootStoreTests(t, func(*testing.T) coldBootTestStore { return state.NewMemStore() })
}

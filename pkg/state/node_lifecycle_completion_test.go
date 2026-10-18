// adr: 1527
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type lifecycleCompletionTestStore interface {
	state.Store
	state.NodeLifecycleCompletionStore
	state.AuditEventOutboxStore
}

func completionNode(t *testing.T, store state.Store, lifecycle state.NodeLifecycle) state.ComputeNode {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "completion-" + uuid.NewString(), Lifecycle: lifecycle, LifecycleGeneration: 999,
		TargetURL: "unix:///run/faas/vmmd.sock", VPCPUs: 4, VCPUBudget: 160,
		MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if node.LifecycleGeneration != 1 {
		t.Fatalf("caller assigned generation: %+v", node)
	}
	return node
}

func completionInput(node state.ComputeNode) state.NodeLifecycleCompletion {
	return state.NodeLifecycleCompletion{NodeID: node.ID, Generation: node.LifecycleGeneration, Lifecycle: node.Lifecycle, DrainedCount: 3, MigratedCount: 2, RecreatedCount: 1}
}

func completionRead(t *testing.T, store state.Store, id string) state.ComputeNode {
	t.Helper()
	node, err := store.NodeGet(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func completionTransition(t *testing.T, store state.Store, node state.ComputeNode, next state.NodeLifecycle) state.ComputeNode {
	t.Helper()
	if err := store.NodeSetLifecycle(t.Context(), node.ID, node.Lifecycle, next); err != nil {
		t.Fatal(err)
	}
	got := completionRead(t, store, node.ID)
	if got.LifecycleGeneration != node.LifecycleGeneration+1 {
		t.Fatalf("transition did not advance generation: %+v -> %+v", node, got)
	}
	return got
}

func completionApplication(t *testing.T, store state.Store) (string, string) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@completion.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "complete-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:completion"})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID, dep.ID
}

func runLifecycleCompletionTests(t *testing.T, create func(*testing.T) lifecycleCompletionTestStore) {
	t.Helper()
	t.Run("generation_all_writers_and_metadata", func(t *testing.T) {
		store := create(t)
		node := completionNode(t, store, state.NodeLifecycleActive)
		node = completionTransition(t, store, node, state.NodeLifecycleDraining)
		// Same-state initiation is a new cycle, even though the lifecycle matches.
		node = completionTransition(t, store, node, state.NodeLifecycleDraining)
		if err := store.HeartbeatComputeNode(t.Context(), node.ID); err != nil {
			t.Fatal(err)
		}
		if got := completionRead(t, store, node.ID); got.LifecycleGeneration != node.LifecycleGeneration {
			t.Fatal("heartbeat changed generation")
		}
		for _, update := range []func(context.Context, state.ComputeNode) (state.ComputeNode, error){store.UpsertComputeNodeFromVmmd, store.UpsertComputeNodeFromOperator} {
			refresh := node
			refresh.LifecycleGeneration = 12345
			refresh.DrainInitiatedAt = nil
			got, err := update(t.Context(), refresh)
			if err != nil || got.LifecycleGeneration != node.LifecycleGeneration {
				t.Fatalf("metadata refresh changed generation: %+v %v", got, err)
			}
			node = completionRead(t, store, node.ID)
			if node.DrainInitiatedAt == nil {
				t.Fatal("refresh erased cycle start")
			}
		}
		// The deprecated upsert reactivates nodes in PostgreSQL. Its lifecycle
		// change must invalidate the drain just like the explicit active setter.
		legacy := node
		legacy.Lifecycle = ""
		legacy.LifecycleGeneration = 12345
		legacyResult, legacyErr := store.UpsertComputeNode(t.Context(), legacy)
		if legacyErr != nil || legacyResult.Lifecycle != state.NodeLifecycleActive || legacyResult.LifecycleGeneration != node.LifecycleGeneration+1 {
			t.Fatalf("legacy registration did not advance generation: %+v %v", legacyResult, legacyErr)
		}
		node = completionRead(t, store, node.ID)
		if err := store.MarkComputeNodeInactive(t.Context(), node.ID); err != nil {
			t.Fatal(err)
		}
		inactive := completionRead(t, store, node.ID)
		if inactive.LifecycleGeneration != node.LifecycleGeneration+1 {
			t.Fatal("inactive writer did not advance generation")
		}
		if err := store.SetComputeNodeActive(t.Context(), node.ID, true); err != nil {
			t.Fatal(err)
		}
		active := completionRead(t, store, node.ID)
		if active.LifecycleGeneration != inactive.LifecycleGeneration+1 {
			t.Fatal("activation did not advance generation")
		}
		active.Lifecycle = state.NodeLifecycleForceDraining
		got, err := store.UpsertComputeNodeFromOperator(t.Context(), active)
		if err != nil || got.LifecycleGeneration != active.LifecycleGeneration+1 {
			t.Fatalf("operator upsert did not advance generation: %+v %v", got, err)
		}
	})
	t.Run("obsolete_uncompleted_cycles", func(t *testing.T) {
		store := create(t)
		for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleForceDraining, state.NodeLifecycleRecovering} {
			node := completionNode(t, store, lifecycle)
			old := completionInput(node)
			node = completionTransition(t, store, node, state.NodeLifecycleUnavailable)
			node = completionTransition(t, store, node, lifecycle)
			if _, err := store.CompleteNodeLifecycle(t.Context(), old); !errors.Is(err, state.ErrNodeLifecycleChanged) {
				t.Fatalf("old %s worker accepted: %v", lifecycle, err)
			}
			if got := completionRead(t, store, node.ID); got.LifecycleGeneration != node.LifecycleGeneration || got.Lifecycle != node.Lifecycle {
				t.Fatalf("stale worker mutated next cycle: %+v", got)
			}
			if _, err := store.ClaimAuditEvent(t.Context(), "test", time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale worker emitted audit: %v", err)
			}
		}
	})
	t.Run("resident_and_recovery_preconditions", func(t *testing.T) {
		store := create(t)
		appID, depID := completionApplication(t, store)
		for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleRecovering} {
			for _, current := range []state.State{state.StateWaking, state.StateColdBooting, state.StateRunning, state.StateDraining, state.StateSnapshotting, state.StateMigrating, state.StateWarm, state.StateFrozen, state.StateParked} {
				node := completionNode(t, store, state.NodeLifecycleActive)
				ins, err := store.CreateInstance(t.Context(), appID, depID, string(current), 128, node.ID, uuid.NewString())
				if err != nil {
					t.Fatalf("seed %s: %v", current, err)
				}
				node = completionTransition(t, store, node, lifecycle)
				r, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node))
				blocked := state.IsLive(string(current))
				if lifecycle == state.NodeLifecycleRecovering {
					blocked = current == state.StateMigrating || current == state.StateSnapshotting
				}
				if blocked && !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s with %s completed: %+v %v", lifecycle, current, r, err)
				}
				if !blocked && err != nil {
					t.Fatalf("%s with %s refused: %v", lifecycle, current, err)
				}
				if err := store.DeleteInstance(t.Context(), ins.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	t.Run("stable_receipt_after_new_cycle_and_outbox_retention", func(t *testing.T) {
		store := create(t)
		for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleForceDraining, state.NodeLifecycleRecovering} {
			node := completionNode(t, store, state.NodeLifecycleActive)
			node = completionTransition(t, store, node, lifecycle)
			in := completionInput(node)
			r, err := store.CompleteNodeLifecycle(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			got := completionRead(t, store, node.ID)
			if r.Replayed || r.AuditOutboxID <= 0 || got.LifecycleGeneration != node.LifecycleGeneration+1 || got.Lifecycle != r.ResultLifecycle || r.CompletedAt.IsZero() || r.InitiatedAt.IsZero() {
				t.Fatalf("incomplete commit: %+v %+v", r, got)
			}
			if lifecycle == state.NodeLifecycleRecovering {
				if !got.Active || got.LastRecoveryOutcome == nil || *got.LastRecoveryOutcome != "succeeded" {
					t.Fatalf("recovery outcome: %+v", got)
				}
			} else if got.Active || got.DrainCompletedAt == nil || !got.DrainCompletedAt.Equal(r.CompletedAt) {
				t.Fatalf("drain outcome: %+v", got)
			}
			for attempt := range 2 {
				delivered, err := store.DeliverAuditEvent(t.Context(), r.AuditOutboxID)
				if err != nil || delivered != (attempt == 0) {
					t.Fatalf("delivery %d: %v %v", attempt, delivered, err)
				}
			}
			if _, err := store.PruneAuditEventOutbox(t.Context(), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if got.Lifecycle != state.NodeLifecycleActive {
				got = completionTransition(t, store, got, state.NodeLifecycleActive)
			}
			got = completionTransition(t, store, got, lifecycle)
			in.DrainedCount = 999 // Retry must retain the original sweep's counts.
			replay, err := store.CompleteNodeLifecycle(t.Context(), in)
			if err != nil || !replay.Replayed {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			replay.Replayed = false
			if !reflect.DeepEqual(replay, r) {
				t.Fatalf("receipt changed: %+v != %+v", replay, r)
			}
			if after := completionRead(t, store, node.ID); after.LifecycleGeneration != got.LifecycleGeneration || after.Lifecycle != got.Lifecycle {
				t.Fatalf("replay changed next cycle: %+v", after)
			}
			if _, err := store.ClaimAuditEvent(t.Context(), "test", time.Minute); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("replay recreated audit: %v", err)
			}
			in.Lifecycle = state.NodeLifecycleUnavailable
			if _, err := store.CompleteNodeLifecycle(t.Context(), in); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid lifecycle accepted: %v", err)
			}
		}
		rows, err := store.ListAllEventsPaged(t.Context(), "schedd", "node.", "", time.Time{}, 100)
		if err != nil || len(rows) != 3 {
			t.Fatalf("completion events=%d %v", len(rows), err)
		}
		for _, row := range rows {
			var payload map[string]any
			if err := json.Unmarshal(row.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if row.Actor != "schedd" || payload["lifecycle_generation"] == nil || payload["result_generation"] == nil {
				t.Fatalf("audit lost generation: %+v", row)
			}
		}
	})
	t.Run("concurrent_controllers", func(t *testing.T) {
		store := create(t)
		node := completionNode(t, store, state.NodeLifecycleDraining)
		type outcome struct {
			r   state.NodeLifecycleCompletionResult
			err error
		}
		results := make(chan outcome, 8)
		var wg sync.WaitGroup
		for range cap(results) {
			wg.Go(func() {
				r, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node))
				results <- outcome{r, err}
			})
		}
		wg.Wait()
		close(results)
		fresh := 0
		var outboxID int64
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if !result.r.Replayed {
				fresh++
			}
			if outboxID != 0 && result.r.AuditOutboxID != outboxID {
				t.Fatal("duplicate completion handoff")
			}
			outboxID = result.r.AuditOutboxID
		}
		if fresh != 1 {
			t.Fatalf("fresh completions=%d", fresh)
		}
	})
	t.Run("legacy_writers_and_invalid_input", func(t *testing.T) {
		store := create(t)
		for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleForceDraining, state.NodeLifecycleRecovering} {
			node := completionNode(t, store, lifecycle)
			if err := store.NodeMarkDrainCompleted(t.Context(), node.ID, time.Now()); err == nil {
				t.Fatal("legacy drain write accepted")
			}
			if err := store.NodeMarkRecovered(t.Context(), node.ID); err == nil {
				t.Fatal("legacy recovery write accepted")
			}
			next := state.NodeLifecycleMaintenance
			if lifecycle == state.NodeLifecycleRecovering {
				next = state.NodeLifecycleActive
			}
			if err := store.NodeSetLifecycle(t.Context(), node.ID, lifecycle, next); err == nil {
				t.Fatal("direct unreceipted completion accepted")
			}
			in := completionInput(node)
			in.Generation = 0
			if _, err := store.CompleteNodeLifecycle(t.Context(), in); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid generation: %v", err)
			}
			in = completionInput(node)
			in.RecreatedCount = -1
			if _, err := store.CompleteNodeLifecycle(t.Context(), in); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid count: %v", err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := store.CompleteNodeLifecycle(cancelled, completionInput(node)); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled completion: %v", err)
			}
		}
	})
}

func TestNodeLifecycleCompletionMemory(t *testing.T) {
	runLifecycleCompletionTests(t, func(*testing.T) lifecycleCompletionTestStore { return state.NewMemStore() })
}

// adr: 1526
package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type nodeIntentTestStore interface {
	state.Store
	state.OperatorNodeStore
	state.NodeLifecycleCompletionStore
	state.AuditEventOutboxStore
}

func seedNodeIntent(t *testing.T, store nodeIntentTestStore, kind state.OperatorIntentKind, lifecycle state.NodeLifecycle) (state.ComputeNode, state.OperatorIntent) {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "node-" + uuid.NewString(), TargetURL: "unix:///run/faas/vmmd.sock", Lifecycle: lifecycle,
		VPCPUs: 2, VCPUBudget: 16, MemMB: 2048, MaxConcurrency: 10, AdmissionCeilingMB: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	return node, claimNodeIntent(t, store, kind, node.ID)
}

func claimNodeIntent(t *testing.T, store nodeIntentTestStore, kind state.OperatorIntentKind, target string) state.OperatorIntent {
	t.Helper()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	id, err := store.InsertOperatorIntent(t.Context(), kind, target, nil, "22222222-2222-2222-2222-222222222222", "planned maintenance", json.RawMessage(`{"source":"test"}`), &traceID)
	if err != nil {
		t.Fatal(err)
	}
	i, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil || i.ID != id {
		t.Fatalf("claim: %+v %v", i, err)
	}
	return i
}

func executeNodeIntent(t *testing.T, store state.OperatorNodeStore, i state.OperatorIntent) state.OperatorNodeResult {
	t.Helper()
	r, err := store.ExecuteOperatorNode(t.Context(), i.ID, i.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func runNodeIntentTests(t *testing.T, create func(*testing.T) nodeIntentTestStore) {
	t.Helper()
	t.Run("transitions_and_durable_failures", func(t *testing.T) {
		for _, tt := range []struct {
			name          string
			kind          state.OperatorIntentKind
			before, after state.NodeLifecycle
			failed        bool
		}{
			{"drain", state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive, state.NodeLifecycleDraining, false},
			{"already_draining", state.OperatorIntentKindNodeDrain, state.NodeLifecycleDraining, state.NodeLifecycleDraining, false},
			{"completed_other_drain", state.OperatorIntentKindNodeDrain, state.NodeLifecycleMaintenance, state.NodeLifecycleMaintenance, true},
			{"force_drain", state.OperatorIntentKindNodeForceDrain, state.NodeLifecycleRecovering, state.NodeLifecycleForceDraining, false},
			{"already_maintenance", state.OperatorIntentKindNodeForceDrain, state.NodeLifecycleMaintenance, state.NodeLifecycleMaintenance, false},
			{"activate", state.OperatorIntentKindNodeActivate, state.NodeLifecycleMaintenance, state.NodeLifecycleActive, false},
			{"activate_unavailable", state.OperatorIntentKindNodeActivate, state.NodeLifecycleUnavailable, state.NodeLifecycleActive, false},
			{"already_active", state.OperatorIntentKindNodeActivate, state.NodeLifecycleActive, state.NodeLifecycleActive, false},
			{"controller_owned", state.OperatorIntentKindNodeActivate, state.NodeLifecycleDraining, state.NodeLifecycleDraining, true},
			{"retire", state.OperatorIntentKindNodeRetire, state.NodeLifecycleMaintenance, state.NodeLifecycleRetired, false},
			{"already_retired", state.OperatorIntentKindNodeRetire, state.NodeLifecycleRetired, state.NodeLifecycleRetired, false},
			{"retired_force_drain", state.OperatorIntentKindNodeForceDrain, state.NodeLifecycleRetired, state.NodeLifecycleRetired, true},
			{"retired_activate", state.OperatorIntentKindNodeActivate, state.NodeLifecycleRetired, state.NodeLifecycleRetired, true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				store := create(t)
				node, i := seedNodeIntent(t, store, tt.kind, tt.before)
				r := executeNodeIntent(t, store, i)
				if (r.Status == state.OperatorIntentFailed) != tt.failed || (r.Error != "") != tt.failed || r.PreviousLifecycle != tt.before || r.Lifecycle != tt.after || r.Changed != (tt.before != tt.after) || r.Replayed || r.AuditOutboxID <= 0 || r.FinishedAt.IsZero() {
					t.Fatalf("result: %+v", r)
				}
				current, err := store.NodeGet(t.Context(), node.ID)
				if err != nil || current.Lifecycle != tt.after || current.Active != tt.after.IsAdmitting() {
					t.Fatalf("node: %+v %v", current, err)
				}
				row, err := store.GetOperatorIntent(t.Context(), i.ID)
				if err != nil || row.Status != r.Status || row.Error != r.Error || row.CompletionAuditOutboxID != r.AuditOutboxID || row.FinishedAt == nil || !row.FinishedAt.Equal(r.FinishedAt) {
					t.Fatalf("intent: %+v %v", row, err)
				}
				replay := executeNodeIntent(t, store, i)
				replay.Replayed = false
				if replay != r {
					t.Fatalf("result changed on replay: %+v != %+v", replay, r)
				}
			})
		}
	})
	t.Run("original_receipt_survives_drain_completion_new_cycle_and_pruning", func(t *testing.T) {
		store := create(t)
		node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
		r := executeNodeIntent(t, store, i)
		current, err := store.NodeGet(t.Context(), node.ID)
		if err != nil {
			t.Fatal(err)
		}
		completion, err := store.CompleteNodeLifecycle(t.Context(), state.NodeLifecycleCompletion{NodeID: node.ID, Generation: current.LifecycleGeneration, Lifecycle: current.Lifecycle})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DeliverAuditEvent(t.Context(), completion.AuditOutboxID); err != nil {
			t.Fatal(err)
		}
		// The retry must not reinterpret maintenance as a failed original drain.
		if replay := executeNodeIntent(t, store, i); !replay.Replayed || replay.Status != state.OperatorIntentSucceeded || replay.Lifecycle != state.NodeLifecycleDraining {
			t.Fatalf("replay after completion: %+v", replay)
		}
		if _, err := store.DeliverAuditEvent(t.Context(), r.AuditOutboxID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PruneAuditEventOutbox(t.Context(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := store.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleMaintenance, state.NodeLifecycleActive); err != nil {
			t.Fatal(err)
		}
		if err := store.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleActive, state.NodeLifecycleForceDraining); err != nil {
			t.Fatal(err)
		}
		before, err := store.NodeGet(t.Context(), node.ID)
		if err != nil {
			t.Fatal(err)
		}
		replay := executeNodeIntent(t, store, i)
		if !replay.Replayed || replay.AuditOutboxID != r.AuditOutboxID || !replay.FinishedAt.Equal(r.FinishedAt) || replay.Lifecycle != r.Lifecycle {
			t.Fatalf("lost original receipt after retention: %+v", replay)
		}
		after, err := store.NodeGet(t.Context(), node.ID)
		if err != nil || after.Lifecycle != before.Lifecycle || !after.DrainInitiatedAt.Equal(*before.DrainInitiatedAt) {
			t.Fatalf("replay changed next cycle: %+v %v", after, err)
		}
		if _, err := store.ClaimAuditEvent(t.Context(), "test", time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("replay recreated pruned audit: %v", err)
		}
		events, err := store.ListEventsByTraceID(t.Context(), *i.TraceID, 100)
		if err != nil || len(events) != 1 {
			t.Fatalf("outcomes=%d %v", len(events), err)
		}
		var payload struct {
			IntentID string            `json:"intent_id"`
			Actor    string            `json:"actor"`
			TraceID  string            `json:"trace_id"`
			Previous string            `json:"previous_lifecycle"`
			Result   string            `json:"result_lifecycle"`
			Changed  bool              `json:"changed"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(events[0].Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.IntentID != i.ID || payload.Actor != i.ActorID || payload.TraceID != *i.TraceID || !payload.Changed || payload.Metadata["source"] != "test" || payload.Previous != string(r.PreviousLifecycle) || payload.Result != string(r.Lifecycle) {
			t.Fatalf("audit attribution: %+v", payload)
		}
	})
	t.Run("reclaim_fences_old_worker_and_unfenced_setters", func(t *testing.T) {
		store := create(t)
		node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
		if n, err := store.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil || n != 1 {
			t.Fatalf("reclaim: %d %v", n, err)
		}
		for attempt := range 2 {
			if _, err := store.ExecuteOperatorNode(t.Context(), i.ID, i.ClaimToken); !errors.Is(err, state.ErrOperatorNodeClaimLost) {
				t.Fatalf("obsolete worker accepted: %v", err)
			}
			if attempt == 0 {
				replacement, err := store.ClaimPendingOperatorIntent(t.Context())
				if err != nil || replacement.ClaimToken == i.ClaimToken {
					t.Fatalf("replacement: %+v %v", replacement, err)
				}
				if err := store.MarkOperatorIntentSucceeded(t.Context(), i.ID, nil); err == nil {
					t.Fatal("unfenced success accepted")
				}
				if err := store.MarkOperatorIntentFailed(t.Context(), i.ID, "stale", nil); err == nil {
					t.Fatal("unfenced failure accepted")
				}
				current, err := store.NodeGet(t.Context(), node.ID)
				if err != nil || current.Lifecycle != state.NodeLifecycleActive {
					t.Fatalf("stale worker changed node: %+v %v", current, err)
				}
				executeNodeIntent(t, store, replacement)
			}
		}
	})
	t.Run("concurrent_commit_has_one_receipt", func(t *testing.T) {
		store := create(t)
		_, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
		type reply struct {
			result state.OperatorNodeResult
			err    error
		}
		replies := make(chan reply, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { r, err := store.ExecuteOperatorNode(t.Context(), i.ID, i.ClaimToken); replies <- reply{r, err} })
		}
		wg.Wait()
		close(replies)
		var original state.OperatorNodeResult
		committed := 0
		for reply := range replies {
			if reply.err != nil {
				t.Fatal(reply.err)
			}
			if !reply.result.Replayed {
				committed++
			}
			reply.result.Replayed = false
			if original.AuditOutboxID == 0 {
				original = reply.result
			}
			if reply.result != original {
				t.Fatalf("different receipts: %+v %+v", reply.result, original)
			}
		}
		if committed != 1 {
			t.Fatalf("commits=%d", committed)
		}
	})

	t.Run("missing_target_stays_failed", func(t *testing.T) {
		for _, target := range []string{uuid.NewString(), "invalid-node-id"} {
			store := create(t)
			i := claimNodeIntent(t, store, state.OperatorIntentKindNodeDrain, target)
			r := executeNodeIntent(t, store, i)
			if r.Status != state.OperatorIntentFailed || r.Changed || r.PreviousLifecycle != "" {
				t.Fatalf("missing target: %+v", r)
			}
			if replay := executeNodeIntent(t, store, i); !replay.Replayed || replay.Error != r.Error || replay.AuditOutboxID != r.AuditOutboxID {
				t.Fatalf("failed receipt changed: %+v", replay)
			}
		}
	})
	t.Run("failure_replay_does_not_act_after_preconditions_change", func(t *testing.T) {
		store := create(t)
		node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleMaintenance)
		r := executeNodeIntent(t, store, i)
		if r.Status != state.OperatorIntentFailed {
			t.Fatalf("expected failure: %+v", r)
		}
		if err := store.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleMaintenance, state.NodeLifecycleActive); err != nil {
			t.Fatal(err)
		}
		replay := executeNodeIntent(t, store, i)
		current, err := store.NodeGet(t.Context(), node.ID)
		if !replay.Replayed || replay.Status != state.OperatorIntentFailed || err != nil || current.Lifecycle != state.NodeLifecycleActive {
			t.Fatalf("failure was re-executed: %+v %+v %v", replay, current, err)
		}
	})
	t.Run("retirement_rechecks_live_instances_and_default_local", func(t *testing.T) {
		store := create(t)
		node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeRetire, state.NodeLifecycleActive)
		account, err := store.CreateAccount(t.Context(), "node-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "node-" + uuid.NewString()[:8], Type: state.AppTypeFunction, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, ImageDigest: "sha256:node-test", Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 128, node.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if err := store.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleActive, state.NodeLifecycleMaintenance); err != nil {
			t.Fatal(err)
		}
		r := executeNodeIntent(t, store, i)
		if r.Status != state.OperatorIntentFailed || r.Changed {
			t.Fatalf("retirement ignored live instance: %+v", r)
		}
		local, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.NodeSetLifecycle(t.Context(), local.ID, local.Lifecycle, state.NodeLifecycleMaintenance); err != nil {
			t.Fatal(err)
		}
		localIntent := claimNodeIntent(t, store, state.OperatorIntentKindNodeRetire, local.ID)
		if r := executeNodeIntent(t, store, localIntent); r.Status != state.OperatorIntentFailed || r.Changed {
			t.Fatalf("retired default-local: %+v", r)
		}
	})
}

func TestOperatorNodeMemory(t *testing.T) {
	runNodeIntentTests(t, func(*testing.T) nodeIntentTestStore { return state.NewMemStore() })
}

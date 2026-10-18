//go:build linux && metal

// spec: §14.A
// adr: 137
// adr: 1527
// adr: 1530
package m9

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// This drill never calls MigrateRecoveryInstance, RecoveryRunner.Tick or
// CompleteNodeLifecycle. The installed schedd daemons own all drain work.
func TestNodeDrainControllerRecovery(t *testing.T) {
	if os.Getenv("FAAS_M9_QUALIFY") != "1" {
		t.Skip("use make native-m9-drain-acceptance on the designated native pair")
	}
	c := nativeConfig(t)
	if c.Drain == nil || os.Getenv("FAAS_M9_SCENARIO") != "drain" {
		t.Fatal("drain gate configuration and scenario required")
	}
	api, err := newDrainAPI(*c.Drain, os.Getenv("FAAS_M9_OPERATOR_TOKEN"))
	require(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(c.TimeoutSeconds)*time.Second)
	t.Cleanup(cancel)
	r := newRuntime(t, ctx, c)
	var original, moved state.Instance
	var before, draining state.ComputeNode
	var receipt fixtureReceipt
	var source state.MigrationSource
	var start time.Time
	query := sqlc.New()
	var completion sqlc.NodeLifecycleCompletion
	params := sqlc.ReadNodeLifecycleCompletionParams{}

	phase(t, "preflight", func(t *testing.T) {
		node, err := r.store.ComputeNodeByName(ctx, c.Source.Name)
		require(t, err)
		require(t, api.preflight(ctx, c.Source.Name, node.ID))
		nodes, err := r.store.ActiveComputeNodes(ctx)
		require(t, err)
		for _, n := range nodes {
			if n.Lifecycle.IsAdmitting() && n.Name != c.Source.Name && n.Name != c.Destination.Name {
				t.Fatal("automatic drain qualification requires an isolated two-node admitting fleet")
			}
		}
		evidence := r.preflight(t, ctx)
		before, err = r.store.ComputeNodeByName(ctx, c.Source.Name)
		require(t, err)
		// A zero-usage request isolates the lifecycle decision from capacity.
		p, err := sched.ChoosePlacement([]state.ComputeNode{before}, nil, nil, sched.Request{RAMMB: 64, VCPU: 1})
		require(t, err)
		if p.NodeID != r.source.nodeID {
			t.Fatal("source was not eligible before drain")
		}
		evidence["source_generation"] = before.LifecycleGeneration
		evidence["previous_drain_completed_at"] = before.DrainCompletedAt
		emitProof(t, "preflight", evidence)
	})
	phase(t, "fixture-boot", func(t *testing.T) {
		wake, err := r.engines[r.source.nodeID].Wake(ctx, c.AppID, c.DeploymentID, state.DefaultEnvScope, "native-m9-drain")
		require(t, err)
		r.instance = wake.InstanceID
		r.ownedWakes[wake.WakeID] = true
		original, err = oneOwner(ctx, r, r.source.nodeID, wake.WakeID)
		require(t, err)
		receipt, err = probe(ctx, &r.source, r.instance, c.FixtureID, uuid.NewString())
		require(t, err)
		emitProof(t, "fixture-boot", map[string]any{"instance_id": r.instance, "wake_id": original.WakeID,
			"node_id": original.NodeID, "probe": receipt})
	})
	phase(t, "drain-pending", func(t *testing.T) {
		paused, err := r.dest.action(ctx, "schedd-pause")
		require(t, err)
		start = time.Now()
		progress, err := api.status(ctx, http.MethodPost, c.Source.Name)
		require(t, err)
		draining, err = r.store.ComputeNodeByName(ctx, c.Source.Name)
		require(t, err)
		if !progress.pending(before.DrainCompletedAt) ||
			!(drainStatus{Lifecycle: string(draining.Lifecycle), InitiatedAt: draining.DrainInitiatedAt, CompletedAt: draining.DrainCompletedAt}).pending(before.DrainCompletedAt) ||
			draining.LifecycleGeneration != before.LifecycleGeneration+1 {
			t.Fatal("real drain request did not start one pending, non-admitting generation")
		}
		params = sqlc.ReadNodeLifecycleCompletionParams{NodeID: pgtype.UUID{Bytes: uuid.MustParse(r.source.nodeID), Valid: true},
			Generation: draining.LifecycleGeneration}
		if _, err := query.ReadNodeLifecycleCompletion(ctx, r.pool, params); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("pending resident drain already has a completion receipt: %v", err)
		}
		if _, err := sched.ChoosePlacement([]state.ComputeNode{draining}, nil, nil, sched.Request{RAMMB: 64, VCPU: 1}); err == nil {
			t.Fatal("production placement policy admitted the draining source")
		}
		// Keep real controllers alive for multiple sweeps while the destination
		// controller is paused. Source vmmd and workload remain available.
		require(t, drainDelay(ctx, 2200*time.Millisecond))
		current, err := r.store.ComputeNodeByName(ctx, c.Source.Name)
		require(t, err)
		_, err = oneOwner(ctx, r, r.source.nodeID, original.WakeID)
		require(t, err)
		ram, err := r.store.ComputeNodeUsedMB(ctx, r.source.nodeID)
		require(t, err)
		cpu, err := r.store.ComputeNodeUsedCPUMillicoresByNode(ctx, []string{r.source.nodeID})
		require(t, err)
		if !(drainStatus{Lifecycle: string(current.Lifecycle), InitiatedAt: current.DrainInitiatedAt, CompletedAt: current.DrainCompletedAt}).pending(before.DrainCompletedAt) ||
			current.LifecycleGeneration != draining.LifecycleGeneration || ram < int64(original.RAMMB) || cpu[r.source.nodeID] <= 0 {
			t.Fatal("drain completed or lost source capacity while its serving workload remained")
		}
		if _, err := query.ReadNodeLifecycleCompletion(ctx, r.pool, params); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("resident barrier acquired a completion receipt: %v", err)
		}
		same, err := probe(ctx, &r.source, r.instance, c.FixtureID, receipt.OperationID)
		require(t, err)
		if same != receipt {
			t.Fatal("pending drain disrupted its serving workload")
		}
		emitProof(t, "drain-pending", map[string]any{"instance_id": r.instance, "source_node_id": r.source.nodeID,
			"generation": draining.LifecycleGeneration, "lifecycle": current.Lifecycle, "placement_refused": true,
			"completion_absent": true, "paused_scheduler": paused, "ram_mb": ram,
			"cpu_millicores": cpu[r.source.nodeID], "probe": same, "observed_completed_at": current.DrainCompletedAt})
	})
	phase(t, "schedd-restart", func(t *testing.T) {
		restart, err := r.dest.action(ctx, "schedd-crash")
		require(t, err)
		restart["drain_generation"] = draining.LifecycleGeneration
		emitProof(t, "schedd-restart", restart)
	})
	phase(t, "automatic-migration", func(t *testing.T) {
		err := awaitDrain(ctx, func() (bool, error) {
			ins, err := r.store.InstanceByID(ctx, r.instance)
			if err != nil {
				return false, err
			}
			if ins.NodeID != r.dest.nodeID || ins.State != string(state.StateRunning) {
				return false, nil
			}
			if ins.WakeID == original.WakeID || !CanonicalUUID(ins.WakeID) || ins.LeaseToken == "" {
				return false, errors.New("automatic drain migration lost its wake or source lease lineage")
			}
			source, err = r.store.MigrationSourceByLease(ctx, ins.LeaseToken)
			if err != nil {
				return false, err
			}
			if source.InstanceID != r.instance || source.SourceNodeID != r.source.nodeID || source.SourceWakeID != original.WakeID {
				return false, errors.New("automatic migration has an unrelated physical source binding")
			}
			moved, err = oneOwner(ctx, r, r.dest.nodeID, ins.WakeID)
			if err == nil {
				r.ownedWakes[ins.WakeID] = true
			}
			return err == nil, err
		})
		require(t, err)
		same, err := probe(ctx, &r.dest, r.instance, c.FixtureID, receipt.OperationID)
		require(t, err)
		if same != receipt {
			t.Fatal("automatic drain cold-booted or duplicated the operation")
		}
		emitProof(t, "automatic-migration", map[string]any{"instance_id": r.instance, "node_id": moved.NodeID,
			"wake_id": moved.WakeID, "source": source, "probe": same, "elapsed_ms": time.Since(start).Milliseconds()})
	})
	phase(t, "drain-completion", func(t *testing.T) {
		var progress drainStatus
		err := awaitDrain(ctx, func() (bool, error) {
			node, err := r.store.ComputeNodeByName(ctx, c.Source.Name)
			if err != nil {
				return false, err
			}
			if node.Lifecycle == state.NodeLifecycleDraining {
				return false, nil
			}
			if node.Lifecycle != state.NodeLifecycleMaintenance || node.LifecycleGeneration != draining.LifecycleGeneration+1 {
				return false, errors.New("drain left its captured generation or did not finish in maintenance")
			}
			// Completion is invalid if native work, ordinary/retained capacity
			// or a deferred source cleanup lease is still present.
			if err := r.empty(ctx, &r.source); err != nil {
				return false, err
			}
			source, err = r.store.MigrationSourceByLease(ctx, moved.LeaseToken)
			if err != nil {
				return false, err
			}
			if source.Phase != state.MigrationSourceRetired || source.Proof == "" {
				return false, errors.New("drain completed without physical source retirement proof")
			}
			completion, err = query.ReadNodeLifecycleCompletion(ctx, r.pool, params)
			if err != nil {
				return false, err
			}
			if completion.SourceLifecycle != "draining" || completion.ResultLifecycle != "maintenance" ||
				completion.ResultGeneration != draining.LifecycleGeneration+1 || completion.AuditOutboxID <= 0 ||
				!completion.CompletedAt.Valid || node.DrainCompletedAt == nil ||
				!completion.CompletedAt.Time.Equal(*node.DrainCompletedAt) {
				return false, errors.New("drain completion is missing its generation-bound receipt and audit handoff")
			}
			progress, err = api.status(ctx, http.MethodGet, c.Source.Name)
			return err == nil, err
		})
		require(t, err)
		if progress.Lifecycle != "maintenance" || progress.CompletedAt == nil ||
			!progress.CompletedAt.Equal(completion.CompletedAt.Time) || !completion.InitiatedAt.Valid ||
			!progress.InitiatedAt.Equal(completion.InitiatedAt.Time) {
			t.Fatal("drain API disagrees with committed completion")
		}
		// Let several real sweeps run again: they must preserve this receipt.
		require(t, drainDelay(ctx, 2200*time.Millisecond))
		replay, err := query.ReadNodeLifecycleCompletion(ctx, r.pool, params)
		require(t, err)
		if replay != completion {
			t.Fatal("controller sweeps replaced the immutable drain completion")
		}
		_, err = oneOwner(ctx, r, r.dest.nodeID, moved.WakeID)
		require(t, err)
		emitProof(t, "drain-completion", map[string]any{"receipt": completion, "source": source, "api": progress,
			"source_empty": true, "receipt_unchanged": true, "elapsed_ms": time.Since(start).Milliseconds()})
	})
	phase(t, "fixture-cleanup", func(t *testing.T) {
		require(t, r.parkOwned(ctx))
		checks := map[string]any{}
		for _, p := range []*nativePeer{&r.source, &r.dest} {
			require(t, r.waitEmpty(ctx, p))
			leaks, err := p.action(ctx, "leaks")
			require(t, err)
			checks[p.config.Name] = leaks
		}
		r.cleaned = true
		emitProof(t, "fixture-cleanup", map[string]any{"instance_id": r.instance, "leaks_after": checks,
			"source_lifecycle": "maintenance"})
	})
}

func awaitDrain(ctx context.Context, check func() (bool, error)) error {
	for {
		ready, err := check()
		if err != nil || ready {
			return err
		}
		if err := drainDelay(ctx, 25*time.Millisecond); err != nil {
			return err
		}
	}
}

func drainDelay(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

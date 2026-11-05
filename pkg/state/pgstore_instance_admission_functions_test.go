//go:build !no_pg

// adr: 1741, 193, 1528, 1523 — a wake's admission in one statement refuses
// exactly as the statement-by-statement transaction does, and takes the same
// per-node lock.
package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func admissionFunctionNode(t *testing.T, store *state.PgStore, ceilingMB int) state.ComputeNode {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "admission-fn-" + uuid.NewString(), TargetURL: "unix:///tmp/admission-fn.sock",
		VPCPUs: 4, MemMB: 8192, MaxConcurrency: 32, AdmissionCeilingMB: ceilingMB, VCPUBudget: 4,
		Lifecycle: state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// Half the admissions take the function, half the statements: the node
// still fits exactly as many as its ceiling allows, so both paths take the
// same per-node advisory lock and read headroom after it.
func TestPgAdmissionFunctionsRaceWithTheStatementPath(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	if !state.AdmissionFunctionsUsable(t.Context(), store) {
		t.Fatal("migrated database lacks the admission functions")
	}
	legacy := state.StatementAdmissionStore(store)
	const (
		ceilingMB = 1024
		admitMB   = 248
		attempts  = 16
		want      = ceilingMB / (admitMB + api.PerVMOverheadMB)
	)
	f := seedRuntimeAppEnv(t, store)
	node := admissionFunctionNode(t, store, ceilingMB)
	dep := f.deployments["production"]
	var start, done sync.WaitGroup
	start.Add(1)
	errs := make([]error, attempts)
	for i := range attempts {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			admit := store
			if i%2 == 1 {
				admit = legacy
			}
			_, errs[i] = admit.CreateInstanceWithMode(t.Context(), f.app.ID, dep.ID, string(state.StateWaking), admitMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
		}()
	}
	start.Done()
	done.Wait()
	admitted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, state.ErrNodeCapacity):
		default:
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	used, err := store.ComputeNodeUsedMB(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if admitted != want || used != ceilingMB {
		t.Fatalf("admitted %d (used %d MB of %d), want %d filling the node exactly", admitted, used, ceilingMB, want)
	}
}

// ADR-909 amendment 1: both admission paths honour a placement binding's
// reservation allowance and clamp it the same way.
func TestPgAdmissionReservationAllowance(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	if !state.AdmissionFunctionsUsable(t.Context(), store) {
		t.Fatal("migrated database lacks the admission functions")
	}
	t.Run("function", func(t *testing.T) { checkReservationAllowance(t, store) })
	t.Run("statements", func(t *testing.T) { checkReservationAllowance(t, state.StatementAdmissionStore(store)) })
}

// Every refusal the transaction makes, the function makes with the same
// error, and neither leaves an instance behind.
func TestPgAdmissionFunctionsRefuseLikeTheStatements(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	for _, path := range []struct {
		name  string
		store *state.PgStore
	}{
		{name: "function", store: store},
		{name: "statements", store: state.StatementAdmissionStore(store)},
	} {
		t.Run(path.name, func(t *testing.T) {
			s := path.store
			f := seedRuntimeAppEnv(t, store)
			dep := f.deployments["production"]
			generation, err := store.AppRuntimeGeneration(t.Context(), f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			admit := func(t *testing.T, ctx context.Context, node state.ComputeNode, ramMB int) (state.Instance, error) {
				t.Helper()
				before, err := store.ListInstancesForApp(ctx, f.app.ID)
				if err != nil {
					t.Fatal(err)
				}
				ins, admitErr := s.CreateInstanceWithMode(ctx, f.app.ID, dep.ID, string(state.StateWaking), ramMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
				if admitErr != nil {
					if after, err := store.ListInstancesForApp(ctx, f.app.ID); err != nil || len(after) != len(before) {
						t.Fatalf("refused admission (%v) left %d instances, want %d", admitErr, len(after), len(before))
					}
				}
				return ins, admitErr
			}
			t.Run("node not admitting", func(t *testing.T) {
				node := admissionFunctionNode(t, store, 8192)
				if err := store.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
					t.Fatal(err)
				}
				if _, err := admit(t, t.Context(), node, 256); !errors.Is(err, state.ErrNodeAdmissionStale) {
					t.Fatalf("admission to a draining node = %v, want ErrNodeAdmissionStale", err)
				}
			})
			t.Run("placement generation moved", func(t *testing.T) {
				node := admissionFunctionNode(t, store, 8192)
				ctx := state.WithNodeAdmission(t.Context(), state.NodeAdmission{NodeID: node.ID, Generation: node.LifecycleGeneration + 1})
				if _, err := admit(t, ctx, node, 256); !errors.Is(err, state.ErrNodeAdmissionStale) {
					t.Fatalf("admission at a stale placement = %v, want ErrNodeAdmissionStale", err)
				}
			})
			t.Run("node full", func(t *testing.T) {
				node := admissionFunctionNode(t, store, 512)
				_, err := admit(t, t.Context(), node, 1024)
				if !errors.Is(err, state.ErrNodeCapacity) || !strings.Contains(err.Error(), "used_mb=") || !strings.Contains(err.Error(), "ceiling_mb=512") {
					t.Fatalf("admission past the ceiling = %v, want ErrNodeCapacity with the headroom", err)
				}
			})
			t.Run("runtime generation moved", func(t *testing.T) {
				node := admissionFunctionNode(t, store, 8192)
				ctx := state.WithRuntimeGenerationGuard(t.Context(), f.app.ID, generation-1)
				if _, err := admit(t, ctx, node, 256); !errors.Is(err, state.ErrRuntimeGenerationMoved) {
					t.Fatalf("admission on a moved generation = %v, want ErrRuntimeGenerationMoved", err)
				}
			})
			t.Run("snapshot invalidated", func(t *testing.T) {
				node := admissionFunctionNode(t, store, 8192)
				r := &state.SnapshotRecoveryAdmission{SnapshotID: uuid.NewString()}
				ctx := state.WithSnapshotRecoveryAdmission(t.Context(), r)
				if _, err := admit(t, ctx, node, 256); !errors.Is(err, state.ErrConflict) || r.Bound() {
					t.Fatalf("admission restoring an unknown snapshot = %v, bound %v; want ErrConflict", err, r.Bound())
				}
			})
			t.Run("admitted with guard and binding", func(t *testing.T) {
				node := admissionFunctionNode(t, store, 8192)
				r := &state.SnapshotRecoveryAdmission{}
				ctx := state.WithSnapshotRecoveryAdmission(state.WithRuntimeGenerationGuard(
					state.WithNodeAdmission(t.Context(), state.NodeAdmission{NodeID: node.ID, Generation: node.LifecycleGeneration}),
					f.app.ID, generation), r)
				ins, err := admit(t, ctx, node, 256)
				if err != nil || !r.Bound() || ins.State != string(state.StateWaking) || ins.NodeID != node.ID {
					t.Fatalf("admission = %+v, %v, bound %v", ins, err, r.Bound())
				}
				if fresh, err := store.InstanceSnapshotRecoveryFresh(t.Context(), ins.ID); err != nil || !fresh {
					t.Fatalf("admitted instance lineage fresh = %v, %v", fresh, err)
				}
			})
		})
	}
}

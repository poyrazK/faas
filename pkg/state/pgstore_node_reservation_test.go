//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStoreNodeReservationSerializesConcurrentAdmits is the test ADR-193
// exists for.
//
// The conformance case proves the ceiling is checked. It cannot prove the
// check is atomic, because it admits one instance at a time — and a
// read-then-insert with no lock passes a sequential test perfectly. The bug
// being fixed only appears when two admissions race: both read the same
// pre-insert total, both conclude there is room, both insert.
//
// So this test races them. The node fits exactly wantAdmitted instances;
// attempts × concurrent callers go at it at once. Without the per-node
// advisory lock in insertInstanceWithNodeReservation, more than
// wantAdmitted rows land and the final sum exceeds the ceiling — which is
// precisely the §6.2-2 violation that a multi-schedd fleet produces under a
// cold burst.
func TestPgStoreNodeReservationSerializesConcurrentAdmits(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)
	ctx := context.Background()

	// 4 × (248 + 8) = 1024 = ceiling. The fifth admission must be refused
	// no matter how the goroutines interleave.
	const (
		ceilingMB     = 1024
		admitMB       = 248
		attempts      = 16
		wantAdmitted  = ceilingMB / (admitMB + api.PerVMOverheadMB) // 4
		wantRefusedAt = attempts - wantAdmitted                     // 12
	)

	acct, err := store.CreateAccount(ctx, "node-reservation-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "node-reservation-" + uuid.NewString(),
		Type:           state.AppTypeApp,
		RAMMB:          admitMB,
		MaxConcurrency: attempts,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:node-reservation",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "reservation-race-" + uuid.NewString(),
		TargetURL:          "unix:///tmp/reservation-race.sock",
		VPCPUs:             4,
		MemMB:              2048,
		MaxConcurrency:     attempts,
		AdmissionCeilingMB: ceilingMB,
		VCPUBudget:         4,
		Lifecycle:          state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}

	// Release every goroutine from the same barrier so the reads overlap.
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			_, err := store.CreateInstance(ctx, app.ID, dep.ID,
				string(state.StateWaking), admitMB, node.ID, uuid.NewString())
			errs[i] = err
		}(i)
	}
	start.Done()
	done.Wait()

	var admitted, refused int
	for i, err := range errs {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, state.ErrNodeCapacity):
			refused++
		default:
			t.Fatalf("attempt %d: unexpected error: %v", i, err)
		}
	}
	if admitted != wantAdmitted {
		t.Errorf("admitted = %d, want %d (over-admission means the per-node lock did not serialize)", admitted, wantAdmitted)
	}
	if refused != wantRefusedAt {
		t.Errorf("refused = %d, want %d", refused, wantRefusedAt)
	}

	// The invariant itself, stated directly: whatever the interleaving did,
	// the node must not be over its ceiling.
	used, err := store.ComputeNodeUsedMB(ctx, node.ID)
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB: %v", err)
	}
	if used > ceilingMB {
		t.Errorf("used = %d MB, exceeds the %d MB ceiling — invariant §6.2-2 violated", used, ceilingMB)
	}
	if used != ceilingMB {
		t.Errorf("used = %d MB, want the node filled to exactly %d MB", used, ceilingMB)
	}
}

// TestPgStoreNodeReservationIsPerNode proves the advisory lock is keyed on
// the node, not taken globally. A fleet whose admissions all serialized
// against one lock would trade the correctness bug for a throughput bug:
// every wake in the fleet would queue behind every other wake.
func TestPgStoreNodeReservationIsPerNode(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)
	ctx := context.Background()

	const (
		ceilingMB = 512
		admitMB   = 248
		nodeCount = 4
	)

	acct, err := store.CreateAccount(ctx, "per-node-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "per-node-" + uuid.NewString(),
		Type:           state.AppTypeApp,
		RAMMB:          admitMB,
		MaxConcurrency: nodeCount * 2,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:per-node",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	nodes := make([]state.ComputeNode, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name:               "per-node-" + uuid.NewString(),
			TargetURL:          "unix:///tmp/per-node.sock",
			VPCPUs:             2,
			MemMB:              1024,
			MaxConcurrency:     4,
			AdmissionCeilingMB: ceilingMB,
			VCPUBudget:         2,
			Lifecycle:          state.NodeLifecycleActive,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode(%d): %v", i, err)
		}
		nodes = append(nodes, node)
	}

	// Each node fits exactly two. Filling all of them concurrently must
	// succeed everywhere: a full node refuses only its own admissions.
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	errs := make([]error, nodeCount*2)
	for i, node := range nodes {
		for j := 0; j < 2; j++ {
			idx := i*2 + j
			done.Add(1)
			go func(idx int, nodeID string) {
				defer done.Done()
				start.Wait()
				_, err := store.CreateInstance(ctx, app.ID, dep.ID,
					string(state.StateWaking), admitMB, nodeID, uuid.NewString())
				errs[idx] = err
			}(idx, node.ID)
		}
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("attempt %d: admission to a node with headroom failed: %v", i, err)
		}
	}
	for i, node := range nodes {
		used, err := store.ComputeNodeUsedMB(ctx, node.ID)
		if err != nil {
			t.Fatalf("ComputeNodeUsedMB(%d): %v", i, err)
		}
		if want := int64(2 * (admitMB + api.PerVMOverheadMB)); used != want {
			t.Errorf("node %d used = %d MB, want %d MB", i, used, want)
		}
	}
}

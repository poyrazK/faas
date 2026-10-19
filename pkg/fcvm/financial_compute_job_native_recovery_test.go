//go:build linux || darwin

package fcvm

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// adr: 1747 — a job stop without a local journal resolves the immutable claim
// by instance. A replacement owner cannot revive the cancelled task's RPC.
func TestFinancialComputeJobCancellationFencesLateNativeCreate(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "native-job-financial@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "native-job", "batch", "example/job", []string{"/bin/true"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	identity := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, identity.Incarnation); err != nil {
		t.Fatal(err)
	}
	instance, token := uuid.NewString(), uuid.NewString()
	if _, err := store.CreateAndClaimJobInstance(ctx, instance, job.ID, run.ID, 0, string(state.StateColdBooting), 128, node.ID, instance, token, time.Now().Add(time.Hour), node.ID); err != nil {
		t.Fatal(err)
	}
	manager, _, _ := nativeManagerFixture(t)
	manager.WithFinancialComputeAuthority(store, node.ID, identity.Incarnation)
	ctx = wire.WithContext(ctx, wire.CorrelationFields{AppID: uuid.NewString(), WakeID: uuid.NewString()})
	if err := manager.Destroy(ctx, instance); err != nil {
		t.Fatalf("unstarted job cancellation: %v", err)
	}
	if retired, err := store.FinancialComputeRetired(ctx, node.ID, instance); err != nil || !retired {
		t.Fatalf("job cancellation not retained: %v %v", retired, err)
	}
	identity.Incarnation = uuid.NewString()
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, identity.Incarnation); err != nil {
		t.Fatal(err)
	}
	manager.WithFinancialComputeAuthority(store, node.ID, identity.Incarnation)
	req := testJobBootRequest(instance)
	req.AccountID = account.ID
	req.RunID = run.ID
	req.LeaseToken = token
	req.NodeID = node.ID
	if _, err := manager.BootJob(ctx, req); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("late job boot acquired new funding: %v", err)
	}
	if manager.LeasedCount() != 0 || manager.LiveCount() != 0 {
		t.Fatal("cancelled job created native effects")
	}
}

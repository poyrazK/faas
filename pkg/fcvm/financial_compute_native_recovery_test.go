//go:build linux || darwin

package fcvm

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A cancelled DB acquire can leave neither a VM journal nor a financial grant.
// Native absence plus a persisted scheduler attempt closes that attempt before
// cleanup acknowledges it; replay remains fenced after the node incarnation changes.
func TestFinancialComputeCancellationFencesLateNativeCreate(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "native-financial@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "native-financial", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	identity := runtimeadmission.Identity{ProtocolVersion: 1, NodeID: node.ID, Incarnation: uuid.NewString()}
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, identity.Incarnation); err != nil {
		t.Fatal(err)
	}
	manager, _, _ := nativeManagerFixture(t)
	manager.WithFinancialComputeAuthority(store, node.ID, identity.Incarnation)
	attempt := uuid.NewString()
	instance := uuid.NewString()
	ctx = wire.WithContext(ctx, wire.CorrelationFields{AppID: app.ID, InstanceID: instance, WakeID: attempt})
	if err := manager.Destroy(ctx, instance); err != nil {
		t.Fatalf("unstarted cancellation: %v", err)
	}
	if retired, err := store.FinancialComputeRetired(ctx, node.ID, instance); err != nil || !retired {
		t.Fatalf("cancellation not retained: %v %v", retired, err)
	}
	identity.Incarnation = uuid.NewString()
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, node.ID, identity.Incarnation); err != nil {
		t.Fatal(err)
	}
	manager.WithFinancialComputeAuthority(store, node.ID, identity.Incarnation)
	req := admissionWake(instance)
	req.AppID = app.ID
	req.AccountID = account.ID
	if _, err := manager.Wake(ctx, req); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("late create acquired fresh authority: %v", err)
	}
	if manager.alloc.InUse() != 0 || manager.LiveCount() != 0 {
		t.Fatal("cancelled financial attempt created native effects")
	}
}

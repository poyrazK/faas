//go:build linux || darwin

package fcvm

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A daemon can die after the physical journal acknowledged cleanup but before
// the funding receipt settled. Native inventory alone then reports no slot.
// Financial recovery must hold that exact slot through any failed settlement.
func TestFinancialComputeRetiredNativeSlotStaysQuarantined(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "settlement_retry"
		if conflict {
			name = "slot_already_reused"
		}
		t.Run(name, func(t *testing.T) {
			m, v, _ := nativeManagerFixture(t)
			lease := prepareRecoveredNative(t, v, 0, "retired-unsettled")
			journal := v.nativeRecovery.journal
			record, err := journal.revoke(t.Context(), lease.Instance)
			if err == nil {
				err = journal.confirmExit(t.Context(), record)
			}
			if err == nil {
				record, err = journal.read(lease.Instance)
			}
			if err == nil {
				err = journal.confirmResourcesRemoved(t.Context(), record)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := m.RecoverNativeProcesses(t.Context()); err != nil || m.LeasedCount() != 0 {
				t.Fatalf("physically retired fixture: %v", err)
			}
			funded := financial.ComputeLease{ID: uuid.NewString(), Fence: uuid.NewString(), Revision: 1,
				AccountID: uuid.NewString(), AppID: uuid.NewString(), InstanceID: lease.Instance, NodeID: "node",
				Incarnation: "old-incarnation", Strict: true, BillableMB: 136, Reserved: 100,
				StartedAt: time.Now().Add(-time.Minute), RunUntil: time.Now().Add(-time.Second)}
			authority := &computeAuthorityFixture{lease: funded, settleErr: errors.New("database unavailable")}
			m.WithFinancialComputeAuthority(authority, "node", "new-incarnation")
			if conflict {
				replacement, err := m.alloc.Acquire("replacement")
				if err != nil || replacement.Slot != lease.Slot {
					t.Fatalf("replacement fixture: %+v %v", replacement, err)
				}
				if err := m.RecoverFinancialComputeLease(t.Context(), funded); err == nil {
					t.Fatal("old funding borrowed the replacement slot")
				}
				if authority.settlements != 0 || m.LeasedCount() != 1 || m.HasInstanceOwnership(lease.Instance) {
					t.Fatal("conflicting recovery performed retirement or settlement")
				}
				return
			}
			if err := m.RecoverFinancialComputeLease(t.Context(), funded); err == nil {
				t.Fatal("fixture did not fail settlement")
			}
			if m.LeasedCount() != 1 || !m.HasInstanceOwnership(lease.Instance) {
				t.Fatal("failed settlement released the retired native slot")
			}
			replacement, err := m.alloc.Acquire("replacement")
			if err != nil || replacement.Slot == lease.Slot {
				t.Fatalf("unsettled slot reused: %+v %v", replacement, err)
			}
			if err := m.alloc.Release(replacement.Instance); err != nil {
				t.Fatal(err)
			}
			authority.mu.Lock()
			authority.settleErr = nil
			authority.mu.Unlock()
			var wg sync.WaitGroup
			var results [2]error
			for i := range results {
				wg.Go(func() { results[i] = m.RecoverFinancialComputeLease(t.Context(), funded) })
			}
			wg.Wait()
			succeeded, fenced := 0, 0
			for _, err := range results {
				if err == nil {
					succeeded++
				} else if errors.Is(err, financial.ErrComputeFence) {
					fenced++
				} else {
					t.Fatal(err)
				}
			}
			if succeeded != 1 || fenced != 1 || authority.settlements != 2 || m.LeasedCount() != 0 || m.HasInstanceOwnership(lease.Instance) {
				t.Fatalf("concurrent recovery did not settle once: %v", results)
			}
			if err := m.RecoverFinancialComputeLease(t.Context(), funded); !errors.Is(err, financial.ErrComputeFence) || m.LeasedCount() != 0 || m.HasInstanceOwnership(lease.Instance) {
				t.Fatalf("stale inventory reconstructed a settled quarantine: %v", err)
			}
		})
	}
}

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

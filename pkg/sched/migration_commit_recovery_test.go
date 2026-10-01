package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type migrationRecoveryFaultStore struct {
	*state.MemStore
	commit            bool
	recoveryErr       error
	cancelAfterCommit context.CancelFunc
	commitAnotherWake bool
}

func (s *migrationRecoveryFaultStore) MigrateInstanceOwner(ctx context.Context, id, from, to, lease string) error {
	if s.commit {
		if err := s.MemStore.MigrateInstanceOwner(ctx, id, from, to, lease); err != nil {
			return err
		}
	}
	return errors.New("legacy database commit reply lost")
}

func (s *migrationRecoveryFaultStore) MigrateInstanceOwnerWithRuntimeConfig(ctx context.Context, id, from, to, lease string, input state.RuntimeConfigMigration) error {
	if s.commit {
		if s.commitAnotherWake {
			input.WakeID = uuid.NewString()
		}
		if err := s.MemStore.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, lease, input); err != nil {
			return err
		}
	}
	if s.cancelAfterCommit != nil {
		s.cancelAfterCommit()
		return context.DeadlineExceeded
	}
	return errors.New("database commit reply lost")
}

func (s *migrationRecoveryFaultStore) ResolveInstanceMigrationCommit(ctx context.Context, attempt state.MigrationCommitAttempt) (state.MigrationCommitResolution, error) {
	if s.recoveryErr != nil {
		return state.MigrationCommitRetained, s.recoveryErr
	}
	if err := ctx.Err(); err != nil {
		return state.MigrationCommitRetained, err
	}
	return s.MemStore.ResolveInstanceMigrationCommit(ctx, attempt)
}

type migrationRecoveryVMM struct{ *migrationReceiptVMM }

func (v *migrationRecoveryVMM) AcknowledgeMigration(ctx context.Context, node, id, lease string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.fakeVMM.AcknowledgeMigration(ctx, node, id, lease)
}

func TestRuntimeConfigMigrationCommitRecoveryPreservesServingDestination(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		commit, canceled, outage, anotherWake bool
		wantSuccess                           bool
		wantUnresolved                        bool
		wantDestroy, wantCancel, wantAck      int
	}{
		{name: "commit reply lost", commit: true, wantSuccess: true, wantAck: 1},
		{name: "request expires after commit", commit: true, canceled: true, wantSuccess: true, wantAck: 1},
		{name: "transaction never committed", wantDestroy: 1, wantCancel: 1},
		{name: "committed but recovery database unavailable", commit: true, outage: true, wantUnresolved: true},
		{name: "uncommitted and recovery database unavailable", outage: true, wantUnresolved: true},
		{name: "another destination wake is preserved", commit: true, anotherWake: true, wantUnresolved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &migrationRecoveryFaultStore{MemStore: state.NewMemStore(), commit: tc.commit, commitAnotherWake: tc.anotherWake}
			if tc.outage {
				store.recoveryErr = errors.New("database unavailable during recovery")
			}
			acct, app, dep := seedApp(t, store.MemStore, api.PlanHobby, 256, 3)
			if err := store.UpsertAppEnvInScope(t.Context(), acct.ID, app.ID, dep.Scope, "MODE", "approved"); err != nil {
				t.Fatal(err)
			}
			vmm := &migrationRecoveryVMM{&migrationReceiptVMM{fakeVMM: &fakeVMM{}, method: vmmdpb.WakeMethod_WAKE_COLD_BOOT}}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			woken, err := engine.Wake(t.Context(), app.ID, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			original, err := store.InstanceByID(t.Context(), woken.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			destination, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "migration-recovery-" + uuid.NewString(), Active: true,
				TargetURL: "tcp://127.0.0.1:50052", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				store.cancelAfterCommit = cancel
			}
			ledger := NewNodeLedger()
			harness := NewMigrationHarness(t.Context(), store, vmm, wire.NewOpsMetrics("schedd"), testLog(), destination.ID, engine.BuildAppSpecForMigration, ledger, nil)
			err = harness.MigrateOne(ctx, original.ID, original.NodeID)
			if (err == nil) != tc.wantSuccess || errors.Is(err, state.ErrMigrationCommitUnresolved) != tc.wantUnresolved {
				t.Fatalf("handoff result: %v, want success=%v unresolved=%v", err, tc.wantSuccess, tc.wantUnresolved)
			}
			if vmm.destroys != tc.wantDestroy || vmm.cancels != tc.wantCancel || vmm.acks != tc.wantAck {
				t.Fatalf("unsafe recovery actions: destroys=%d cancels=%d acks=%d", vmm.destroys, vmm.cancels, vmm.acks)
			}
			current, err := store.InstanceByID(t.Context(), original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.commit {
				if current.NodeID != destination.ID || current.State != string(state.StateRunning) || current.WakeID == original.WakeID {
					t.Fatalf("recovery reverted committed ownership: %+v", current)
				}
				inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), current.ID)
				if err != nil || !exists || inputs.Variables["MODE"] != "approved" {
					t.Fatalf("recovery discarded committed runtime inputs: %+v %v %v", inputs, exists, err)
				}
			} else if tc.outage {
				if current.State != string(state.StateMigrating) || current.NodeID != original.NodeID || current.LeaseToken == "" {
					t.Fatalf("unresolved recovery discarded its durable lease: %+v", current)
				}
			} else if current.State != string(state.StateParked) || current.NodeID != original.NodeID || current.LeaseToken != "" {
				t.Fatalf("known abort was not fenced before VM cleanup: %+v", current)
			}
			if reserved := ledger.ResidentRAMForNode(destination.ID) > 0; reserved != (tc.wantDestroy == 0) {
				t.Fatalf("destination reservation retained=%v after recovery", reserved)
			}
		})
	}
}

func TestRuntimeConfigMigrationLegacyOwnershipCommitRecovery(t *testing.T) {
	store := &migrationRecoveryFaultStore{MemStore: state.NewMemStore(), commit: true}
	instanceID := seedInstanceForMigration(t, store.MemStore, "dying")
	vmm := &fakeVMM{}
	ledger := NewNodeLedger()
	harness := NewMigrationHarness(t.Context(), store, vmm, wire.NewOpsMetrics("schedd"), testLog(), "new-owner", stubSpecBuilder, ledger, nil)
	if err := harness.MigrateOne(t.Context(), instanceID, "dying"); err != nil {
		t.Fatal(err)
	}
	if vmm.destroys != 0 || vmm.cancels != 0 || vmm.acks != 1 || ledger.ResidentRAMForNode("new-owner") == 0 {
		t.Fatalf("legacy lost reply destroyed committed capacity: destroys=%d cancels=%d acks=%d", vmm.destroys, vmm.cancels, vmm.acks)
	}
}

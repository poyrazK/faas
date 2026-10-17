package state

// adr: 1547. Independent migration protocols must not spend the same capacity.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type functionMigrationCapacityTestStore interface {
	Store
	functionDestinationTestStore
	MigrationSourceStore
	MigrationAdmissionStore
}

func genericSourceForFunctionCapacity(t *testing.T, s functionMigrationCapacityTestStore, dest ComputeNode) (Instance, MigrationAdmissionPlan) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@migration-capacity.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "migration-capacity-" + uuid.NewString(), RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:capacity-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	source := dest
	source.ID, source.Name = "", "migration-source-"+uuid.NewString()
	source.AdmissionCeilingMB = api.RAMAdmissionCeilingMB
	source, err = s.UpsertComputeNode(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(StateRunning), 128, source.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	lease := MigrationLease{InstanceID: ins.ID, SourceNodeID: source.ID, LeaseToken: uuid.NewString(), CreatedAt: time.Now(), LeaseExpiresAt: time.Now().Add(time.Minute), Pending: true}
	if err := s.ReserveMigrationLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindMigrationSource(ctx, MigrationSourceBinding{InstanceID: ins.ID, SourceNodeID: source.ID, SourceWakeID: ins.WakeID, LeaseToken: lease.LeaseToken, NativeGeneration: uuid.NewString(), PhysicalBinding: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteMigrationLease(ctx, lease.LeaseToken, "capacity/memory", "capacity/state"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(ctx, ins.ID, source.ID, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	return ins, MigrationAdmissionPlan{ID: uuid.NewString(), InstanceID: ins.ID, SourceNodeID: source.ID, DestinationNodeID: dest.ID,
		SourceWakeID: ins.WakeID, DestinationWakeID: uuid.NewString(), LeaseToken: lease.LeaseToken, MemStorageKey: "capacity/memory", VMStateStorageKey: "capacity/state",
		RequestDigest: strings.Repeat("b", 64), DestinationGeneration: dest.LifecycleGeneration, RAMMB: 128 + api.PerVMOverheadMB,
		CPUMillicores: api.DefaultAppCPUMillicores, VCPU: api.MustLimitsFor(api.PlanPro).VCPU, ExpiresAt: time.Now().Add(30 * time.Second).UTC().Truncate(time.Microsecond)}
}

func functionMigrationSharedCapacity(t *testing.T, s functionMigrationCapacityTestStore) {
	ctx := t.Context()
	ins, g, _, _ := functionDestinationFixture(t, s)
	node, err := s.ComputeNodeByID(ctx, g.Binding.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = ins.RAMMB + api.PerVMOverheadMB
	node, err = s.UpsertComputeNode(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	_, plan := genericSourceForFunctionCapacity(t, s, node)
	if used, err := s.ComputeNodeUsedMB(ctx, node.ID); err != nil || used < int64(ins.RAMMB+api.PerVMOverheadMB) {
		t.Fatal("scheduler projection omitted the pending function destination", used, err)
	}
	if _, err := s.ReserveMigrationAdmission(ctx, plan); !errors.Is(err, ErrNodeCapacity) {
		t.Fatal("generic migration spent a pending function destination's RAM", err)
	}
	if _, err := s.FenceFunctionMigrationSource(ctx, ins.ID, g.Source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveMigrationAdmission(ctx, plan); !errors.Is(err, ErrNodeCapacity) {
		t.Fatal("source fencing released unconfirmed destination capacity", err)
	}
	if err := s.RetireFunctionMigrationDestination(ctx, g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveMigrationAdmission(ctx, plan); err != nil {
		t.Fatal("confirmed function retirement retained shared capacity", err)
	}
}

func functionMigrationCommittedSourceCapacity(t *testing.T, s functionMigrationCapacityTestStore) {
	ctx := t.Context()
	ins, g, receipt, input := functionDestinationFixture(t, s)
	if err := s.ReadyFunctionMigrationDestination(ctx, g, receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input); err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByID(ctx, ins.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = ins.RAMMB + api.PerVMOverheadMB
	if _, err := s.UpsertComputeNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	other, _ := genericSourceForFunctionCapacity(t, s, node)
	if used, err := s.ComputeNodeUsedMB(ctx, node.ID); err != nil || used < int64(ins.RAMMB+api.PerVMOverheadMB) {
		t.Fatal("scheduler projection omitted the committed physical source", used, err)
	}
	if _, err := s.CreateInstance(ctx, other.AppID, other.DeploymentID, string(StateWaking), 128, node.ID, uuid.NewString()); !errors.Is(err, ErrNodeCapacity) {
		t.Fatal("ownership transfer released the unretired physical source", err)
	}
	// This trusted cleanup acknowledgement follows the physical source retire RPC.
	if err := s.DeleteMigrationLease(ctx, g.Source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInstance(ctx, other.AppID, other.DeploymentID, string(StateWaking), 128, node.ID, uuid.NewString()); err != nil {
		t.Fatal("confirmed source cleanup retained physical capacity", err)
	}
}

func TestFunctionMigrationSharedCapacityMem(t *testing.T) {
	t.Run("independent reservations", func(t *testing.T) { functionMigrationSharedCapacity(t, NewMemStore()) })
	t.Run("committed source cleanup", func(t *testing.T) { functionMigrationCommittedSourceCapacity(t, NewMemStore()) })
}

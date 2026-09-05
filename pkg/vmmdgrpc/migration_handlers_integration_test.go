package vmmdgrpc

import (
	"context"
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type migrationHandlerVMM struct {
	vmmStubBase
	snapshots int
	resumes   int
	destroys  int
	wakeReq   fcvm.WakeRequest
}

// migrationSnapshotOnlyVMM models a partially wired vmmd. Keeping the
// snapshot seam without the resume seam must fail closed: otherwise a
// successful Prepare followed by a Cancel could drop the only reference to a
// paused source VM.
type migrationSnapshotOnlyVMM struct {
	vmmStubBase
}

func (f *migrationSnapshotOnlyVMM) SnapshotKeepAlive(_ context.Context, _ string, _ fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	return fcvm.SnapshotInfo{}, nil
}

func (f *migrationHandlerVMM) SnapshotKeepAlive(_ context.Context, _ string, _ fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	f.snapshots++
	return fcvm.SnapshotInfo{MemBytes: 128, VMStateBytes: 64}, nil
}

func (f *migrationHandlerVMM) ResumeVM(_ context.Context, _ string) error {
	f.resumes++
	return nil
}

func (f *migrationHandlerVMM) Wake(_ context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
	f.wakeReq = req
	return &fcvm.Instance{
		Lease: fcvm.Lease{
			HostIP: netip.MustParseAddr("10.100.0.9"),
			UID:    20009,
		},
		Net: netns.Config{Netns: "fc-migrated"},
	}, nil
}

func (f *migrationHandlerVMM) Destroy(_ context.Context, _ string) error {
	f.destroys++
	return nil
}

func seedMigrationHandlerInstance(t *testing.T, store *state.MemStore) string {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "migration-handler@"+t.Name(), api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		ID:        "app-migration-handler-" + t.Name(),
		AccountID: acct.ID,
		Slug:      "migration-handler-" + t.Name(),
		NodeID:    "source",
		Status:    state.AppActive,
		RAMMB:     128,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 128, "source", "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return ins.ID
}

func TestMigrationHandlers_RestoreAndSourceLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	vmm := &migrationHandlerVMM{}
	s := New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithMigrationStore(store)
	instanceID := seedMigrationHandlerInstance(t, store)

	prepared, err := s.PrepareLiveMigration(ctx, &vmmdpb.PrepareLiveMigrationRequest{
		InstanceId:         instanceID,
		SnapshotStorageKey: "snap/" + instanceID + "/mem",
	})
	if err != nil {
		t.Fatalf("PrepareLiveMigration: %v", err)
	}
	if vmm.snapshots != 1 {
		t.Fatalf("SnapshotKeepAlive calls = %d, want 1", vmm.snapshots)
	}
	if prepared.GetFcVersion() != "1.10.0" {
		t.Fatalf("fc_version = %q, want 1.10.0", prepared.GetFcVersion())
	}

	if err := store.MarkInstanceMigrating(ctx, instanceID, "source", prepared.GetLeaseToken()); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	adopted, err := s.AdoptMigratedInstance(ctx, &vmmdpb.AdoptMigratedInstanceRequest{
		InstanceId:        instanceID,
		AppSpec:           &vmmdpb.AppSpec{BaseKey: "base/node22", LayerKey: "layer/app", VcpuCount: 2, MemSizeMib: 128, AppId: "app-id"},
		MemStorageKey:     prepared.GetMemStorageKey(),
		VmstateStorageKey: prepared.GetVmstateStorageKey(),
		LeaseToken:        prepared.GetLeaseToken(),
		Plan:              string(api.PlanHobby),
		AccountId:         "acct-id",
		DeploymentId:      "deployment-id",
		FcVersion:         prepared.GetFcVersion(),
	})
	if err != nil {
		t.Fatalf("AdoptMigratedInstance: %v", err)
	}
	if adopted.GetHostIp() != "10.100.0.9" || adopted.GetNetns() != "fc-migrated" || adopted.GetGuestUid() != 20009 {
		t.Fatalf("adopted response = %+v, want destination network identifiers", adopted)
	}
	if vmm.wakeReq.Snapshot == nil || vmm.wakeReq.Snapshot.StorageKey != prepared.GetMemStorageKey() ||
		vmm.wakeReq.Snapshot.VMStateStorageKey != prepared.GetVmstateStorageKey() ||
		vmm.wakeReq.Snapshot.FCVersion != prepared.GetFcVersion() {
		t.Fatalf("wake snapshot = %+v, want prepared snapshot metadata", vmm.wakeReq.Snapshot)
	}
	if vmm.wakeReq.Plan != api.PlanHobby || vmm.wakeReq.AccountID != "acct-id" ||
		vmm.wakeReq.DeploymentID != "deployment-id" || vmm.wakeReq.AppID != "app-id" {
		t.Fatalf("wake context = plan=%q account=%q deployment=%q app=%q, want migration context",
			vmm.wakeReq.Plan, vmm.wakeReq.AccountID, vmm.wakeReq.DeploymentID, vmm.wakeReq.AppID)
	}

	if _, err := s.AcknowledgeMigration(ctx, &vmmdpb.AcknowledgeMigrationRequest{
		InstanceId: instanceID, LeaseToken: prepared.GetLeaseToken(),
	}); err != nil {
		t.Fatalf("AcknowledgeMigration: %v", err)
	}
	if vmm.destroys != 1 {
		t.Fatalf("source Destroy calls = %d, want 1", vmm.destroys)
	}
}

func TestMigrationHandlers_CancelResumesBeforeDroppingLease(t *testing.T) {
	ctx := context.Background()
	vmm := &migrationHandlerVMM{}
	s := New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	if err := s.migrations.put(&activeMigration{instanceID: "cancel-me", leaseToken: "cancel-token"}); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	if _, err := s.CancelLiveMigration(ctx, &vmmdpb.CancelLiveMigrationRequest{
		InstanceId: "cancel-me", LeaseToken: "cancel-token",
	}); err != nil {
		t.Fatalf("CancelLiveMigration: %v", err)
	}
	if vmm.resumes != 1 {
		t.Fatalf("ResumeVM calls = %d, want 1", vmm.resumes)
	}
	if _, err := s.migrations.get("cancel-me", "cancel-token"); err == nil {
		t.Fatal("cancel lease remains after successful resume")
	}
}

func TestMigrationHandlers_RequireResumeCapability(t *testing.T) {
	ctx := context.Background()
	vmm := &migrationSnapshotOnlyVMM{}
	s := New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)

	_, err := s.PrepareLiveMigration(ctx, &vmmdpb.PrepareLiveMigrationRequest{
		InstanceId:         "resume-required",
		SnapshotStorageKey: "snap/resume-required/mem",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("PrepareLiveMigration without ResumeVM code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if _, getErr := s.migrations.get("resume-required", "any"); getErr == nil {
		t.Fatal("PrepareLiveMigration retained a lease despite missing resume capability")
	}

	if err := s.migrations.put(&activeMigration{instanceID: "cancel-resume-required", leaseToken: "cancel-token"}); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	_, err = s.CancelLiveMigration(ctx, &vmmdpb.CancelLiveMigrationRequest{
		InstanceId: "cancel-resume-required", LeaseToken: "cancel-token",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("CancelLiveMigration without ResumeVM code = %v, want %v", status.Code(err), codes.Unimplemented)
	}
	if _, getErr := s.migrations.get("cancel-resume-required", "cancel-token"); getErr != nil {
		t.Fatalf("CancelLiveMigration dropped lease without ResumeVM: %v", getErr)
	}
}

func TestMigrationHandlers_ExpiredPrePhase2MigrationResumesSource(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	vmm := &migrationHandlerVMM{}
	s := New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).
		WithMigrationStore(store).
		WithNodeID("source")
	instanceID := seedMigrationHandlerInstance(t, store)

	err := s.cleanupExpiredMigration(ctx, &activeMigration{
		instanceID: instanceID,
		leaseToken: "expired-lease",
	})
	if err != nil {
		t.Fatalf("cleanupExpiredMigration: %v", err)
	}
	if vmm.resumes != 1 || vmm.destroys != 0 {
		t.Fatalf("source cleanup = resumes=%d destroys=%d, want 1/0", vmm.resumes, vmm.destroys)
	}
}

func TestMigrationHandlers_ExpiredCommittedMigrationDestroysSource(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	vmm := &migrationHandlerVMM{}
	s := New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).
		WithMigrationStore(store).
		WithNodeID("source")
	instanceID := seedMigrationHandlerInstance(t, store)
	if err := store.MarkInstanceMigrating(ctx, instanceID, "source", "peer-lease"); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	if err := store.MigrateInstanceOwner(ctx, instanceID, "source", "destination", "peer-lease"); err != nil {
		t.Fatalf("MigrateInstanceOwner: %v", err)
	}

	err := s.cleanupExpiredMigration(ctx, &activeMigration{
		instanceID: instanceID,
		leaseToken: "expired-lease",
	})
	if err != nil {
		t.Fatalf("cleanupExpiredMigration: %v", err)
	}
	if vmm.destroys != 1 || vmm.resumes != 0 {
		t.Fatalf("source cleanup = resumes=%d destroys=%d, want 0/1", vmm.resumes, vmm.destroys)
	}
}

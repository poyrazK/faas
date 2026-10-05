package vmmdgrpc

// adr: 628
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
	"github.com/onebox-faas/faas/pkg/wire"
)

type regionalMigrationVMM struct {
	migrationHandlerVMM
	record                *runtimequotahost.MigrationRecord
	captureErr, deleteErr error
	regionalResumes       int
	skipCompletion        bool
}

func (v *regionalMigrationVMM) SnapshotKeepAlive(_ context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	v.snapshots++
	v.record = &runtimequotahost.MigrationRecord{Version: 1, InstanceKey: instance, LeaseToken: spec.MigrationLeaseToken, Source: spec.RegionalMigrationSource.Clone(), MemStorageKey: spec.StorageKey, VMStateStorageKey: spec.VMStateStorageKey}
	return fcvm.SnapshotInfo{}, v.captureErr
}
func (v *regionalMigrationVMM) RegionalMigrationSource(_ context.Context, instance, token string) (runtimequotahost.MigrationRecord, error) {
	if v.record == nil || v.record.LeaseToken != token {
		return runtimequotahost.MigrationRecord{}, runtimequotahost.ErrMigrationNotFound
	}
	if v.record.InstanceKey != instance {
		return runtimequotahost.MigrationRecord{}, runtimequotahost.ErrBinding
	}
	return *v.record, nil
}
func (v *regionalMigrationVMM) ResumeRegionalMigration(_ context.Context, instance, token string, p runtimequota.Placement) error {
	if v.record == nil || v.record.InstanceKey != instance || v.record.LeaseToken != token || v.record.Source.Permit != p.Permit {
		return runtimequotahost.ErrBinding
	}
	if !v.record.Completed && !v.skipCompletion {
		v.regionalResumes++
		v.record.Completed = true
		v.record.Resume = &p
	}
	return nil
}

func TestRegionalMigrationNativeReturnWithoutDurableCompletionCannotAcknowledge(t *testing.T) {
	v := &regionalMigrationVMM{skipCompletion: true}
	s := New(v, wire.NewOpsMetrics("regional_migration_completion_test"), "1.10.0", nil)
	p := regionalMigrationPlacement(t)
	raw, _ := json.Marshal(p)
	prepared, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: p.Permit.InstanceID, SnapshotStorageKey: "snap/source/mem", RegionalRuntime: raw})
	if err != nil {
		t.Fatal(err)
	}
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	nextRaw, _ := json.Marshal(next)
	if _, err := s.CancelLiveMigration(t.Context(), &vmmdpb.CancelLiveMigrationRequest{InstanceId: p.Permit.InstanceID, LeaseToken: prepared.LeaseToken, RegionalRuntime: nextRaw}); err == nil {
		t.Fatal("native return without persisted completion acknowledged")
	}
	if _, err := s.migrations.get(p.Permit.InstanceID, prepared.LeaseToken); err != nil || v.deletes != 0 {
		t.Fatal("unconfirmed completion discarded lease or capture", err)
	}
}
func (v *regionalMigrationVMM) DeleteWarmSnapshot(_ context.Context, mem, vmstate string) error {
	v.deletes++
	v.deletedMemKey = mem
	v.deletedVMStateKey = vmstate
	return v.deleteErr
}

func regionalMigrationPlacement(t *testing.T) runtimequota.Placement {
	t.Helper()
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2, Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "migration", APIBase: "https://cell.example.com"}}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
	return runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
}

func TestRegionalMigrationRPCDoesNotUseLegacyCleanup(t *testing.T) {
	v := &regionalMigrationVMM{}
	s := New(v, wire.NewOpsMetrics("regional_migration_test"), "1.10.0", nil)
	p := regionalMigrationPlacement(t)
	raw, _ := json.Marshal(p)
	prepared, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: p.Permit.InstanceID, SnapshotStorageKey: "snap/source/mem", RegionalRuntime: raw})
	if err != nil || !bytes.Equal(prepared.GetRegionalRuntime(), raw) {
		t.Fatal("capture source acknowledgement", err)
	}
	legacy := &vmmdpb.CancelLiveMigrationRequest{InstanceId: p.Permit.InstanceID, LeaseToken: prepared.GetLeaseToken()}
	if _, err := s.CancelLiveMigration(t.Context(), legacy); err == nil {
		t.Fatal("legacy cancel bypassed original source")
	}
	if _, err := s.AcknowledgeMigration(t.Context(), &vmmdpb.AcknowledgeMigrationRequest{InstanceId: legacy.InstanceId, LeaseToken: legacy.LeaseToken}); err == nil {
		t.Fatal("legacy ack retired regional source")
	}
	if err := s.cleanupExpiredMigration(t.Context(), &activeMigration{instanceID: legacy.InstanceId, leaseToken: legacy.LeaseToken}); err == nil {
		t.Fatal("expiry resumed regional source")
	}
	if v.resumes != 0 || v.destroys != 0 {
		t.Fatal("legacy native cleanup executed")
	}
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	nextRaw, _ := json.Marshal(next)
	req := &vmmdpb.CancelLiveMigrationRequest{InstanceId: legacy.InstanceId, LeaseToken: legacy.LeaseToken, RegionalRuntime: nextRaw}
	v.deleteErr = errors.New("snapshot cleanup temporarily unavailable")
	if _, err := s.CancelLiveMigration(t.Context(), req); err == nil {
		t.Fatal("failed cleanup acknowledged")
	}
	if _, err := s.migrations.get(legacy.InstanceId, legacy.LeaseToken); err != nil {
		t.Fatal("failed cleanup lost lease", err)
	}
	v.deleteErr = nil
	expired := &activeMigration{instanceID: legacy.InstanceId, leaseToken: legacy.LeaseToken}
	if err := s.cleanupExpiredMigration(t.Context(), expired); err != nil || expired.memKey != "snap/source/mem" || v.regionalResumes != 1 {
		t.Fatal("completed expiry repeated VM work or lost cleanup keys", err)
	}
	for range 2 {
		resp, err := s.CancelLiveMigration(t.Context(), req)
		if err != nil || resp.GetInstanceId() != req.InstanceId || resp.GetLeaseToken() != req.LeaseToken || !bytes.Equal(resp.GetRegionalRuntime(), req.RegionalRuntime) {
			t.Fatal("exact completion replay", err)
		}
	}
	if v.regionalResumes != 1 || v.resumes != 0 || v.deletedMemKey != "snap/source/mem" || v.deletedVMStateKey != "snap/source/vmstate" {
		t.Fatal("completion replay lost binding or repeated resume")
	}
	req.LeaseToken = "unknown"
	if _, err := s.CancelLiveMigration(t.Context(), req); err == nil {
		t.Fatal("unknown regional lease acknowledged")
	}
}

func TestRegionalMigrationCaptureFailureRetainsLeaseAndSource(t *testing.T) {
	failure := errors.New("uncertain capture")
	v := &regionalMigrationVMM{captureErr: failure}
	s := New(v, wire.NewOpsMetrics("regional_migration_failure_test"), "1.10.0", nil)
	p := regionalMigrationPlacement(t)
	raw, _ := json.Marshal(p)
	if _, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: p.Permit.InstanceID, SnapshotStorageKey: "snap/source/mem", RegionalRuntime: raw}); err == nil {
		t.Fatal("capture failure acknowledged")
	}
	if v.record == nil {
		t.Fatal("capture did not retain source")
	}
	retained, err := s.migrations.get(p.Permit.InstanceID, v.record.LeaseToken)
	if err != nil || retained.leaseToken != v.record.LeaseToken || !retained.pending || v.resumes != 0 {
		t.Fatal("failed capture lost lease/source or resumed implicitly")
	}
	legacy := &migrationHandlerVMM{}
	old := New(legacy, wire.NewOpsMetrics("regional_migration_legacy_test"), "1.10.0", nil)
	if _, err := old.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: p.Permit.InstanceID, SnapshotStorageKey: "snap/source/mem", RegionalRuntime: raw}); err == nil || legacy.snapshots != 0 {
		t.Fatal("missing capability fell back to legacy capture")
	}
}

func TestRegionalMigrationRejectsMalformedMetadataBeforeHostWork(t *testing.T) {
	for _, raw := range [][]byte{[]byte("null"), []byte(`{"Permit":{}}`), make([]byte, api.RegionalRuntimeHostRecordMaxBytes+1)} {
		v := &regionalMigrationVMM{}
		s := New(v, wire.NewOpsMetrics("regional_migration_validation_test"), "1.10.0", nil)
		if _, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: uuid.NewString(), SnapshotStorageKey: "snap/source/mem", RegionalRuntime: raw}); err == nil {
			t.Fatal("malformed source capture accepted")
		}
		if _, err := s.CancelLiveMigration(t.Context(), &vmmdpb.CancelLiveMigrationRequest{InstanceId: uuid.NewString(), LeaseToken: "lease", RegionalRuntime: raw}); err == nil {
			t.Fatal("malformed rollback accepted")
		}
		if v.snapshots != 0 || v.regionalResumes != 0 || v.resumes != 0 {
			t.Fatal("invalid request reached host work")
		}
	}
}

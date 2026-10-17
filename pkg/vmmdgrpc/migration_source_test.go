// adr: 1530
package vmmdgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type sourceAckFault struct {
	state.MigrationSourceStore
	before, after bool
}

func (s *sourceAckFault) AcknowledgeMigrationSource(ctx context.Context, b state.MigrationSourceBinding, outcome, proof string) error {
	if s.before {
		return errors.New("source acknowledgement write failed")
	}
	if err := s.MigrationSourceStore.AcknowledgeMigrationSource(ctx, b, outcome, proof); err != nil {
		return err
	}
	if s.after {
		return errors.New("source acknowledgement reply lost")
	}
	return nil
}

func commitIncomingForSourceTest(t *testing.T, f incomingMigrationFixture) {
	t.Helper()
	if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err != nil {
		t.Fatal(err)
	}
	ctx := state.WithMigrationAdmission(t.Context(), f.plan.ID)
	ctx = state.WithNodeAdmission(ctx, state.NodeAdmission{NodeID: f.plan.DestinationNodeID, Generation: f.plan.DestinationGeneration})
	if err := f.store.MigrateInstanceOwnerWithRuntimeConfig(ctx, f.plan.InstanceID, f.plan.SourceNodeID, f.plan.DestinationNodeID, f.plan.LeaseToken, state.RuntimeConfigMigration{ExpectedWakeID: f.plan.SourceWakeID, WakeID: f.plan.DestinationWakeID}); err != nil {
		t.Fatal(err)
	}
}

func sourceServerForTest(f incomingMigrationFixture) *Server {
	return New(f.vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithMigrationStore(f.store).WithNodeID(f.plan.SourceNodeID)
}

func TestMigrationSourceAcknowledgementRequiresOwnershipCommit(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	s := sourceServerForTest(f)
	ack := &vmmdpb.AcknowledgeMigrationRequest{InstanceId: f.plan.InstanceID, LeaseToken: f.plan.LeaseToken}
	if _, err := s.AcknowledgeMigration(t.Context(), ack); err == nil || f.vmm.destroys != 0 {
		t.Fatal("early acknowledgement destroyed serving source", err)
	}
	commitIncomingForSourceTest(t, f)
	f.vmm.retireErr = errors.New("source physical cleanup unavailable")
	if _, err := s.AcknowledgeMigration(t.Context(), ack); err == nil || f.vmm.destroys != 0 {
		t.Fatal("failed cleanup acknowledged source", err)
	}
	if used, err := f.store.ComputeNodeUsedMB(t.Context(), f.plan.SourceNodeID); err != nil || used == 0 {
		t.Fatalf("failed cleanup erased capacity: %d %v", used, err)
	}
	if _, err := f.store.GetMigrationLease(t.Context(), f.plan.InstanceID, f.plan.LeaseToken); err != nil {
		t.Fatal("failed cleanup dropped lease", err)
	}
	f.vmm.retireErr = nil
	if _, err := s.AcknowledgeMigration(t.Context(), ack); err != nil || f.vmm.destroys != 1 {
		t.Fatal("retry could not retire original", err)
	}
}

func TestMigrationSourceLostAcknowledgementReplaysAfterDaemonReplacement(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	commitIncomingForSourceTest(t, f)
	s := sourceServerForTest(f)
	s.migrationSources = &sourceAckFault{MigrationSourceStore: f.store, after: true}
	ack := &vmmdpb.AcknowledgeMigrationRequest{InstanceId: f.plan.InstanceID, LeaseToken: f.plan.LeaseToken}
	if _, err := s.AcknowledgeMigration(t.Context(), ack); err == nil || f.vmm.destroys != 1 {
		t.Fatal("lost reply returned success", err)
	}
	f.vmm.retireErr = errors.New("replacement VM must be preserved")
	next := sourceServerForTest(f)
	if _, err := next.AcknowledgeMigration(t.Context(), ack); err != nil || f.vmm.destroys != 1 {
		t.Fatal("receipt replay touched a replacement", err)
	}
	if _, err := next.AcknowledgeMigration(t.Context(), ack); err != nil || f.vmm.destroys != 1 {
		t.Fatal("stale reply lost durable retirement", err)
	}
}

func TestMigrationSourceRecoveryRunsBeforeLeaseExpiryAndRetainsFailures(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	commitIncomingForSourceTest(t, f)
	s := sourceServerForTest(f)
	f.vmm.retireErr = errors.New("cleanup failed")
	s.reconcileMigrationSources(t.Context(), time.Now())
	if s.migrationSourceAfter != f.plan.LeaseToken {
		t.Fatal("failed source starved recovery cursor")
	}
	r, err := f.store.MigrationSourceByLease(t.Context(), f.plan.LeaseToken)
	if err != nil || r.Phase != state.MigrationSourceRetiring {
		t.Fatalf("failed recovery lost source: %+v %v", r, err)
	}
	next := sourceServerForTest(f) // Empty tracker; the lease is not expired.
	f.vmm.retireErr = nil
	next.reconcileMigrationSources(t.Context(), time.Now())
	r, err = f.store.MigrationSourceByLease(t.Context(), f.plan.LeaseToken)
	if err != nil || r.Phase != state.MigrationSourceRetired || f.vmm.destroys != 1 {
		t.Fatalf("restart failed to reconcile source: %+v %v", r, err)
	}
	if _, err := f.store.GetMigrationLease(t.Context(), f.plan.InstanceID, f.plan.LeaseToken); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retired lease retained after recovery: %v", err)
	}
	if f.vmm.deletedMemKey != f.plan.MemStorageKey || f.vmm.deletedVMStateKey != f.plan.VMStateStorageKey {
		t.Fatal("recovery lost source snapshot cleanup handles")
	}
}

func TestMigrationSourceWrongReceiverAndLateCancelNeverTouchVM(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	commitIncomingForSourceTest(t, f)
	ack := &vmmdpb.AcknowledgeMigrationRequest{InstanceId: f.plan.InstanceID, LeaseToken: f.plan.LeaseToken}
	if _, err := f.server.AcknowledgeMigration(t.Context(), ack); err == nil || f.vmm.destroys != 0 {
		t.Fatal("another receiver retired source", err)
	}
	s := sourceServerForTest(f)
	if _, err := s.CancelLiveMigration(t.Context(), &vmmdpb.CancelLiveMigrationRequest{InstanceId: f.plan.InstanceID, LeaseToken: f.plan.LeaseToken}); err == nil || f.vmm.resumes != 0 {
		t.Fatal("late cancel resumed committed source", err)
	}
}

func TestMigrationSourceParkedRollbackRetiresOriginalWithoutResuming(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	if _, err := f.store.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.plan.DestinationNodeID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CancelInstanceMigration(t.Context(), f.plan.InstanceID, f.plan.SourceNodeID, f.plan.LeaseToken); err != nil {
		t.Fatal(err)
	}
	s := sourceServerForTest(f)
	if _, err := s.CancelLiveMigration(t.Context(), &vmmdpb.CancelLiveMigrationRequest{InstanceId: f.plan.InstanceID, LeaseToken: f.plan.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	if f.vmm.resumes != 0 || f.vmm.destroys != 1 {
		t.Fatal("parked rollback retained a resident source VM")
	}
	if used, err := f.store.ComputeNodeUsedMB(t.Context(), f.plan.SourceNodeID); err != nil || used != 0 {
		t.Fatalf("parked source resources remain: %d %v", used, err)
	}
}

func sourcePreparationInstance(t *testing.T, f incomingMigrationFixture) state.Instance {
	t.Helper()
	old, err := f.store.MigrationInstanceByID(t.Context(), f.plan.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	i, err := f.store.CreateInstance(t.Context(), old.AppID, old.DeploymentID, "running", 128, f.plan.SourceNodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestMigrationSourcePreparePersistsAuthorityAndKeysBeforeCapture(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	i := sourcePreparationInstance(t, f)
	s := sourceServerForTest(f)
	var captured state.MigrationSourceBinding
	f.vmm.sourceSnapshot = func(b state.MigrationSourceBinding, spec fcvm.SnapshotSpec) error {
		captured = b
		r, err := f.store.MigrationSourceByLease(t.Context(), b.LeaseToken)
		if err != nil || r.MigrationSourceBinding != b || r.Phase != state.MigrationSourceBound {
			t.Fatalf("capture preceded binding: %+v %v", r, err)
		}
		l, err := f.store.GetMigrationLease(t.Context(), b.InstanceID, b.LeaseToken)
		if err != nil || !l.Pending || l.MemStorageKey != spec.StorageKey || l.VMStateStorageKey != spec.VMStateStorageKey {
			t.Fatalf("capture preceded cleanup metadata: %+v %v", l, err)
		}
		return errors.New("capture reply lost after pause")
	}
	if _, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: i.ID, SnapshotStorageKey: "source-test/mem"}); err == nil || captured.InstanceID != i.ID {
		t.Fatal("capture failure was not returned", err)
	}
	if _, err := f.store.GetMigrationLease(t.Context(), i.ID, captured.LeaseToken); err != nil {
		t.Fatal("uncertain capture dropped recovery lease", err)
	}
	if _, err := s.CancelLiveMigration(t.Context(), &vmmdpb.CancelLiveMigrationRequest{InstanceId: i.ID, LeaseToken: captured.LeaseToken}); err != nil || f.vmm.resumes != 1 {
		t.Fatal("uncertain capture could not resume original", err)
	}
	if f.vmm.deletedMemKey != "source-test/mem" || f.vmm.deletedVMStateKey != "source-test/vmstate" {
		t.Fatal("partial capture lost cleanup handles")
	}
}

type sourceBindingFault struct {
	state.MigrationSourceStore
	committed bool
}

func (s *sourceBindingFault) BindMigrationSource(ctx context.Context, b state.MigrationSourceBinding) (state.MigrationSource, error) {
	if s.committed {
		if _, err := s.MigrationSourceStore.BindMigrationSource(ctx, b); err != nil {
			return state.MigrationSource{}, err
		}
	}
	return state.MigrationSource{}, errors.New("initial source binding write lost")
}

func TestMigrationSourceLostInitialBindingWriteRecoversWithoutCapture(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	i := sourcePreparationInstance(t, f)
	s := sourceServerForTest(f)
	s.migrationSources = &sourceBindingFault{MigrationSourceStore: f.store, committed: true}
	if _, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: i.ID, SnapshotStorageKey: "source-test/mem"}); err == nil {
		t.Fatal("failed binding write returned prepare success")
	}
	var lease string
	for token, binding := range f.vmm.sourceBindings {
		if binding.InstanceID == i.ID {
			lease = token
		}
	}
	if lease == "" || f.vmm.snapshots != 0 {
		t.Fatal("capture ran without durable source binding")
	}
	next := sourceServerForTest(f)
	m, err := next.migrationLeaseForPhase(t.Context(), i.ID, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.cleanupExpiredMigration(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	r, err := f.store.MigrationSourceByLease(t.Context(), lease)
	if err != nil || r.Phase != state.MigrationSourceResumed || f.vmm.snapshots != 0 || f.vmm.resumes != 1 {
		t.Fatalf("lost binding write replayed capture: %+v %v", r, err)
	}
}

func TestMigrationSourceFailedPrepareWithoutBindingReleasesOnlyUnstartedLease(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	i := sourcePreparationInstance(t, f)
	s := sourceServerForTest(f)
	s.migrationSources = &sourceBindingFault{MigrationSourceStore: f.store}
	if _, err := s.PrepareLiveMigration(t.Context(), &vmmdpb.PrepareLiveMigrationRequest{InstanceId: i.ID, SnapshotStorageKey: "source-test/mem"}); err == nil {
		t.Fatal("unbound prepare succeeded")
	}
	for token, b := range f.vmm.sourceBindings {
		if b.InstanceID != i.ID {
			continue
		}
		if _, err := f.store.GetMigrationLease(t.Context(), i.ID, token); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unstarted prepare stranded lease: %v", err)
		}
		if _, err := s.migrations.get(i.ID, token); err == nil {
			t.Fatal("unstarted prepare stranded cache entry")
		}
	}
	if f.vmm.snapshots != 0 {
		t.Fatal("capture ran before durable binding")
	}
}

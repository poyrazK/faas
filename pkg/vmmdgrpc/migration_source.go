package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type migrationSourceVMM interface {
	MigrationAdmissionAvailable(string) bool
	BindMigrationSource(context.Context, state.MigrationSourceBinding) (state.MigrationSourceBinding, error)
	SnapshotMigrationSource(context.Context, state.MigrationSourceBinding, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	FinishMigrationSource(context.Context, state.MigrationSource, bool) (string, string, error)
}

func (s *Server) durableMigrationSource() bool {
	node, err := uuid.Parse(s.nodeID)
	return err == nil && node != uuid.Nil && s.migrationStore != nil
}

// Capture requires a retained source binding. Its absence proves that this
// prepare never dispatched capture; the FK also fences a late binding commit.
func (s *Server) releaseUnboundMigrationPrepare(ctx context.Context, lease string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	if _, err := s.migrationSources.MigrationSourceByLease(cleanupCtx, lease); !errors.Is(err, state.ErrNotFound) {
		return
	}
	if err := s.deleteDurableMigrationLease(cleanupCtx, lease); err == nil && s.migrations != nil {
		s.migrations.deleteByLeaseToken(lease)
	}
}

func (s *Server) retireExpiredMigrationSource(ctx context.Context, m *activeMigration) error {
	if s.durableMigrationSource() {
		return s.finishMigrationSource(ctx, m.instanceID, m.leaseToken, true)
	}
	return s.destroyMigrationVM(ctx, m.instanceID)
}

// The local binding is durable before its first database write. Recover that
// narrow lost-write window without replaying capture, even after lease expiry.
func (s *Server) recoverMigrationSourceBinding(ctx context.Context, m *activeMigration) (state.MigrationSource, error) {
	r, err := s.migrationSources.MigrationSourceByLease(ctx, m.leaseToken)
	if !errors.Is(err, state.ErrNotFound) {
		return r, err
	}
	vmm, ok := s.vmm.(migrationSourceVMM)
	if !ok {
		return r, fmt.Errorf("vmmd: source binding recovery unavailable")
	}
	reader, ok := s.migrationStore.(migrationInstanceReader)
	if !ok {
		return r, fmt.Errorf("vmmd: source binding reader unavailable")
	}
	i, err := reader.MigrationInstanceByID(ctx, m.instanceID)
	if err != nil {
		return r, err
	}
	if i.NodeID != s.nodeID {
		return r, state.ErrConflict
	}
	b, err := vmm.BindMigrationSource(ctx, state.MigrationSourceBinding{InstanceID: i.ID, SourceNodeID: s.nodeID, SourceWakeID: i.WakeID, LeaseToken: m.leaseToken})
	if err != nil {
		return r, err
	}
	return s.migrationSources.BindMigrationSource(ctx, b)
}

func (s *Server) prepareMigrationSource(ctx context.Context, m *activeMigration, spec fcvm.SnapshotSpec) error {
	vmm, ok := s.vmm.(migrationSourceVMM)
	if !ok || s.migrationSources == nil || !vmm.MigrationAdmissionAvailable(s.nodeID) {
		return fmt.Errorf("vmmd: durable native migration source unavailable")
	}
	reader, ok := s.migrationStore.(migrationInstanceReader)
	if !ok {
		return fmt.Errorf("vmmd: durable migration source reader unavailable")
	}
	i, err := reader.MigrationInstanceByID(ctx, m.instanceID)
	if err != nil {
		return err
	}
	if i.NodeID != s.nodeID {
		return state.ErrConflict
	}
	b, err := vmm.BindMigrationSource(ctx, state.MigrationSourceBinding{InstanceID: i.ID, SourceNodeID: s.nodeID, SourceWakeID: i.WakeID, LeaseToken: m.leaseToken})
	if err != nil {
		return err
	}
	if _, err := s.migrationSources.BindMigrationSource(ctx, b); err != nil {
		return err
	}
	if err := s.migrationSources.RecordMigrationSourceSnapshot(ctx, m.leaseToken, s.nodeID, spec.StorageKey, spec.VMStateStorageKey); err != nil {
		return err
	}
	// Keep the cache useful after a partial capture too. Recovery reads the
	// durable lease if this daemon dies before publishing snapshot completion.
	s.migrations.mu.Lock()
	m.memKey, m.vmstateKey = spec.StorageKey, spec.VMStateStorageKey
	s.migrations.mu.Unlock()
	_, err = vmm.SnapshotMigrationSource(ctx, b, spec)
	return err
}

// A pending record is terminal only after vmmd supplies physical proof. The
// proof is persisted locally before state acknowledgement, allowing replay after
// any lost reply or daemon replacement without another instance-name operation.
func (s *Server) finishMigrationSource(ctx context.Context, instance, lease string, retire bool) error {
	ctx, cancel := context.WithTimeout(ctx, api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	if s.migrationSources == nil {
		return fmt.Errorf("vmmd: durable migration source store unavailable")
	}
	r, err := s.migrationSources.MigrationSourceByLease(ctx, lease)
	if err != nil {
		return err
	}
	if r.InstanceID != instance || r.SourceNodeID != s.nodeID || retire && r.Phase == state.MigrationSourceResumed {
		return state.ErrConflict
	}
	if !r.Pending() {
		return nil
	}
	r, retire, err = s.authorizeMigrationSourceAction(ctx, r, retire)
	if err != nil {
		return err
	}
	vmm, ok := s.vmm.(migrationSourceVMM)
	if !ok {
		return fmt.Errorf("vmmd: generation-bound source cleanup unavailable")
	}
	outcome, proof, err := vmm.FinishMigrationSource(ctx, r, retire)
	if err != nil {
		return err
	}
	if retire && outcome != state.MigrationSourceRetired {
		return fmt.Errorf("vmmd: source retirement lacks physical cleanup")
	}
	ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer ackCancel()
	return s.migrationSources.AcknowledgeMigrationSource(ackCtx, r.MigrationSourceBinding, outcome, proof)
}

// Resume is valid only while the original wake remains a serving owner.
func (s *Server) authorizeMigrationSourceAction(ctx context.Context, r state.MigrationSource, retire bool) (state.MigrationSource, bool, error) {
	if retire {
		next, err := s.migrationSources.RequestMigrationSourceRetirement(ctx, r.LeaseToken, s.nodeID)
		return next, true, err
	}
	if r.Phase != state.MigrationSourceBound || s.migrationAdmissions == nil {
		return r, false, state.ErrConflict
	}
	safe, err := s.migrationAdmissions.FenceMigrationSourceResume(ctx, r.InstanceID, r.LeaseToken)
	if err != nil {
		return r, false, err
	}
	if !safe {
		return r, false, state.ErrMigrationAdoptionUnresolved
	}
	reader, ok := s.migrationStore.(migrationInstanceReader)
	if !ok {
		return r, false, fmt.Errorf("vmmd: source owner reader unavailable")
	}
	i, err := reader.MigrationInstanceByID(ctx, r.InstanceID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return r, false, err
	}
	if i.NodeID == r.SourceNodeID && i.WakeID == r.SourceWakeID && (i.State == string(state.StateRunning) || i.State == string(state.StateDraining)) {
		return r, false, nil
	}
	// A parked rollback owns zero RAM and must retire its original physical VM.
	next, err := s.migrationSources.RequestMigrationSourceRetirement(ctx, r.LeaseToken, s.nodeID)
	return next, true, err
}

func (s *Server) reconcileMigrationSources(ctx context.Context, now time.Time) {
	if s.migrationSources == nil || !s.durableMigrationSource() {
		return
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	rows, err := s.migrationSources.ListMigrationSourceLeasesForRecovery(recoveryCtx, s.nodeID, now, s.migrationSourceAfter, api.MigrationAdmissionRecoveryBatchMax)
	if err != nil {
		if s.log != nil {
			s.log.Warn("vmmd: source migration recovery read failed", "err", err)
		}
		return
	}
	if len(rows) == 0 {
		s.migrationSourceAfter = ""
		return
	}
	for _, r := range rows {
		if recoveryCtx.Err() != nil {
			break
		}
		s.migrationSourceAfter = r.LeaseToken
		m := &activeMigration{instanceID: r.InstanceID, leaseToken: r.LeaseToken, createdAt: r.CreatedAt, leaseExpiresAt: r.LeaseExpiresAt, memKey: r.MemStorageKey, vmstateKey: r.VMStateStorageKey, pending: r.Pending}
		err := s.cleanupExpiredMigration(recoveryCtx, m)
		if err == nil {
			err = s.deleteMigrationSnapshot(recoveryCtx, m)
		}
		if err == nil {
			err = s.deleteDurableMigrationLease(recoveryCtx, r.LeaseToken)
		}
		if err == nil && s.migrations != nil {
			s.migrations.deleteByLeaseToken(r.LeaseToken)
		}
		if err != nil && !errors.Is(err, context.Canceled) && s.log != nil {
			s.log.Warn("vmmd: source migration cleanup retained", "instance_id", r.InstanceID, "err", err)
		}
	}
}

package vmmdgrpc

// adr: 1545. vmmd reads schedd's fence; only vmmd touches the physical source.

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type functionMigrationVMM interface {
	FunctionMigrationSourceRequired(string) bool
	PrepareFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding, fcvm.SnapshotSpec, func(context.Context) error) (fcvm.SnapshotInfo, error)
	RecoverFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding, func(context.Context) error) (bool, error)
	RetireFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding) error
	ForgetFunctionMigrationSource(fcvm.FunctionMigrationBinding)
}

func functionSourceBinding(source state.FunctionMigrationSource) fcvm.FunctionMigrationBinding {
	return fcvm.FunctionMigrationBinding{InstanceID: source.InstanceID, LeaseToken: source.LeaseToken, SourceNodeID: source.SourceNodeID, SourceWakeID: source.SourceWakeID}
}

func (s *Server) functionSource(ctx context.Context, id, token string) (state.FunctionMigrationSource, bool, error) {
	store, ok := s.migrationStore.(state.FunctionMigrationSourceStore)
	if !ok {
		return state.FunctionMigrationSource{}, false, nil
	}
	source, err := store.GetFunctionMigrationSource(ctx, id, token)
	if errors.Is(err, state.ErrNotFound) {
		return state.FunctionMigrationSource{}, false, nil
	}
	return source, err == nil, err
}

func (s *Server) prepareFunctionSource(ctx context.Context, m *activeMigration, spec fcvm.SnapshotSpec) (bool, error) {
	vmm, ok := s.vmm.(functionMigrationVMM)
	if !ok || !vmm.FunctionMigrationSourceRequired(m.instanceID) {
		return false, nil
	}
	store, ok := s.migrationStore.(state.FunctionMigrationSourceStore)
	reader, readable := s.migrationStore.(migrationInstanceReader)
	if !functionlifetime.NativeQualificationBuild || !ok || !readable || s.migrationLeases == nil || s.nodeID == "" {
		return false, runtimeadmission.ErrUnavailable
	}
	ins, err := reader.MigrationInstanceByID(ctx, m.instanceID)
	if err != nil {
		return false, err
	}
	if ins.NodeID != s.nodeID || ins.WakeID == "" || ins.State != string(state.StateRunning) || ins.LeaseToken != "" {
		return false, runtimeadmission.ErrStale
	}
	source := state.FunctionMigrationSource{InstanceID: m.instanceID, LeaseToken: m.leaseToken, SourceNodeID: s.nodeID, SourceWakeID: ins.WakeID}
	if admitted, ok := s.vmm.(admittedFunctionMigrationVMM); ok && admitted.FunctionMigrationSourceAdmissionRequired(m.instanceID) {
		return s.prepareAdmittedFunctionSource(ctx, m, spec, source, admitted)
	}
	registrationAttempted := false
	_, err = vmm.PrepareFunctionMigrationSource(ctx, functionSourceBinding(source), spec, func(ctx context.Context) error {
		registrationAttempted = true
		return store.RegisterFunctionMigrationSource(ctx, source, spec.StorageKey, spec.VMStateStorageKey)
	})
	if err != nil {
		return registrationAttempted, err
	}
	return true, store.PrepareFunctionMigrationSource(ctx, m.instanceID, m.leaseToken)
}

func (s *Server) authorizeFunctionSourceRecovery(ctx context.Context, source state.FunctionMigrationSource) error {
	current, found, err := s.functionSource(ctx, source.InstanceID, source.LeaseToken)
	if err != nil {
		return err
	}
	if !found || current.SourceNodeID != source.SourceNodeID || current.SourceWakeID != source.SourceWakeID || current.Phase != state.FunctionMigrationRecovering {
		return runtimeadmission.ErrStale
	}
	reader, ok := s.migrationStore.(migrationInstanceReader)
	if !ok {
		return runtimeadmission.ErrUnavailable
	}
	ins, err := reader.MigrationInstanceByID(ctx, source.InstanceID)
	if err != nil {
		return err
	}
	if ins.NodeID != source.SourceNodeID || ins.WakeID != source.SourceWakeID || ins.State != string(state.StateMigrating) || ins.LeaseToken != state.FunctionMigrationRollbackToken(source.LeaseToken) {
		return runtimeadmission.ErrStale
	}
	return ctx.Err()
}

func (s *Server) cancelFunctionSource(ctx context.Context, m *activeMigration, source state.FunctionMigrationSource) (*vmmdpb.CancelLiveMigrationResponse, error) {
	vmm, ok := s.vmm.(functionMigrationVMM)
	if !ok || s.nodeID != source.SourceNodeID {
		return nil, runtimeadmission.ErrUnavailable
	}
	if source.Phase == state.FunctionMigrationRecovered || source.Phase == state.FunctionMigrationRetired {
		// schedd already committed the physical result. Cleanup retries do not
		// invoke a resource callback or touch any current VM.
		if err := s.deleteMigrationSnapshot(ctx, m); err != nil {
			return nil, err
		}
		if err := s.deleteDurableMigrationLease(ctx, m.leaseToken); err != nil {
			return nil, err
		}
		if s.migrations != nil {
			s.migrations.deleteByLeaseToken(m.leaseToken)
		}
		return &vmmdpb.CancelLiveMigrationResponse{SourceProtocolVersion: fcvm.FunctionMigrationSourceProtocolVersion, SourceRetired: source.Phase == state.FunctionMigrationRetired}, nil
	}
	if m.pending {
		return nil, runtimeadmission.ErrUnavailable
	}
	if err := s.authorizeFunctionSourceRecovery(ctx, source); err != nil {
		return nil, err
	}
	var retired bool
	var err error
	if source.CaptureToken != "" {
		retired, err = s.recoverAdmittedFunctionSource(ctx, source)
	} else {
		retired, err = vmm.RecoverFunctionMigrationSource(ctx, functionSourceBinding(source), func(ctx context.Context) error {
			return s.authorizeFunctionSourceRecovery(ctx, source)
		})
	}
	if err != nil {
		if source.CaptureToken != "" && refusedFunctionSourceRecovery(err) {
			return s.retireRefusedFunctionSource(ctx, source, vmm)
		}
		return nil, err
	}
	// Keep the lease and capture until schedd commits RUNNING or STOPPED.
	return &vmmdpb.CancelLiveMigrationResponse{SourceProtocolVersion: fcvm.FunctionMigrationSourceProtocolVersion, SourceRetired: retired}, nil
}

func (s *Server) retireFunctionSource(ctx context.Context, m *activeMigration, source state.FunctionMigrationSource) error {
	vmm, ok := s.vmm.(functionMigrationVMM)
	reader, readable := s.migrationStore.(migrationInstanceReader)
	if !ok || !readable || m.pending || source.SourceNodeID != s.nodeID {
		return runtimeadmission.ErrUnavailable
	}
	ins, err := reader.MigrationInstanceByID(ctx, source.InstanceID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	if err == nil && (ins.NodeID == "" || ins.NodeID == source.SourceNodeID && ins.WakeID != source.SourceWakeID) {
		return runtimeadmission.ErrStale
	}
	if err == nil && ins.NodeID == source.SourceNodeID && ins.State != string(state.StateStopped) && ins.State != string(state.StateFailed) && ins.State != string(state.StateEvictingAccountDeleting) {
		return runtimeadmission.ErrUnavailable
	}
	return vmm.RetireFunctionMigrationSource(ctx, functionSourceBinding(source))
}

func (s *Server) cleanupExpiredFunctionSource(ctx context.Context, m *activeMigration, source state.FunctionMigrationSource) error {
	if source.Phase == state.FunctionMigrationRecovered || source.Phase == state.FunctionMigrationRetired {
		return nil
	}
	reader, ok := s.migrationStore.(migrationInstanceReader)
	if !ok {
		return runtimeadmission.ErrUnavailable
	}
	ins, err := reader.MigrationInstanceByID(ctx, source.InstanceID)
	if errors.Is(err, state.ErrNotFound) || err == nil && ins.NodeID != "" && ins.NodeID != source.SourceNodeID {
		return s.retireFunctionSource(ctx, m, source)
	}
	if err != nil {
		return err
	}
	if _, err := s.cancelFunctionSource(ctx, m, source); err != nil {
		return err
	}
	// Only schedd may complete the ownership row. Never remove recovery
	// authority while its result still needs that durable commit.
	return fmt.Errorf("function migration awaits scheduler ownership completion: %w", runtimeadmission.ErrUnavailable)
}

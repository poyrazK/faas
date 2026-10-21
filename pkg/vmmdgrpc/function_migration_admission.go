package vmmdgrpc

// adr: 20261018030614. The authenticated source owner obtains current durable permission.

import (
	"context"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type admittedFunctionMigrationVMM interface {
	FunctionMigrationSourceAdmissionRequired(string) bool
	PrepareAdmittedFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding, fcvm.SnapshotSpec,
		func(context.Context, runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error),
		func(context.Context, runtimeadmission.FunctionMigrationGrant) error) (fcvm.SnapshotInfo, error)
	RecoverAdmittedFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding, runtimeadmission.FunctionMigrationGrant,
		func(context.Context, runtimeadmission.FunctionMigrationGrant) error) (bool, error)
}

func (s *Server) prepareAdmittedFunctionSource(ctx context.Context, m *activeMigration, spec fcvm.SnapshotSpec, source state.FunctionMigrationSource, vmm admittedFunctionMigrationVMM) (bool, error) {
	store, ok := s.migrationStore.(state.FunctionMigrationAdmissionStore)
	if !ok {
		return false, runtimeadmission.ErrUnavailable
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return false, err
	}
	registrationAttempted := false
	var permission runtimeadmission.FunctionMigrationGrant
	info, err := vmm.PrepareAdmittedFunctionMigrationSource(ctx, functionSourceBinding(source), spec,
		func(ctx context.Context, parent runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error) {
			if parent.Binding.NodeID != s.nodeID || parent.Binding.InstanceID != m.instanceID {
				return runtimeadmission.FunctionMigrationGrant{}, runtimeadmission.ErrStale
			}
			registrationAttempted = true
			grant, err := store.IssueFunctionMigrationCapture(ctx, state.FunctionMigrationCaptureRequest{
				Source: source, Parent: parent, MemoryKey: spec.StorageKey, VMStateKey: spec.VMStateStorageKey, FCVersion: s.fcVer,
			})
			permission = grant.Clone()
			return grant, err
		}, store.CheckFunctionMigrationCapture)
	if err != nil {
		return registrationAttempted, err
	}
	destinations, ok := s.migrationStore.(state.FunctionMigrationDestinationStore)
	if !ok {
		return true, runtimeadmission.ErrUnavailable
	}
	return true, destinations.RecordFunctionMigrationCapture(ctx, permission, info.Capture)
}

func (s *Server) recoverAdmittedFunctionSource(ctx context.Context, source state.FunctionMigrationSource) (bool, error) {
	vmm, ok := s.vmm.(admittedFunctionMigrationVMM)
	store, writable := s.migrationStore.(state.FunctionMigrationAdmissionStore)
	if !functionlifetime.NativeQualificationBuild || !ok || !writable {
		return false, runtimeadmission.ErrUnavailable
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return false, err
	}
	grant, err := store.IssueFunctionMigrationRecovery(ctx, source.InstanceID, source.LeaseToken)
	if err != nil {
		return false, err
	}
	return vmm.RecoverAdmittedFunctionMigrationSource(ctx, functionSourceBinding(source), grant,
		func(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant) error {
			if err := store.CheckFunctionMigrationRecovery(ctx, grant); err != nil {
				return err
			}
			return s.authorizeFunctionSourceRecovery(ctx, source)
		})
}

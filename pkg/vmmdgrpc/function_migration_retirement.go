package vmmdgrpc

// adr: 1577. A rollback fence can authorize retirement, never stale resume.

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func refusedFunctionSourceRecovery(err error) bool {
	return errors.Is(err, state.ErrApplicationStandardRuntimeStale) || errors.Is(err, runtimeadmission.ErrStale) || errors.Is(err, runtimeadmission.ErrExpired)
}

func (s *Server) retireRefusedFunctionSource(ctx context.Context, source state.FunctionMigrationSource, vmm functionMigrationVMM) (*vmmdpb.CancelLiveMigrationResponse, error) {
	if !functionlifetime.NativeQualificationBuild {
		return nil, runtimeadmission.ErrUnavailable
	}
	if s.nodeID != source.SourceNodeID {
		return nil, runtimeadmission.ErrStale
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.authorizeFunctionSourceRecovery(ctx, source); err != nil {
		return nil, err
	}
	destinations, ok := s.migrationStore.(state.FunctionMigrationDestinationStore)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	d, err := destinations.GetFunctionMigrationDestination(ctx, source.InstanceID, source.LeaseToken)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	if err == nil && d.Phase != state.FunctionDestinationRetired {
		return nil, runtimeadmission.ErrStale
	}
	if err := vmm.RetireFunctionMigrationSource(ctx, functionSourceBinding(source)); err != nil {
		return nil, err
	}
	return &vmmdpb.CancelLiveMigrationResponse{SourceProtocolVersion: fcvm.FunctionMigrationSourceProtocolVersion, SourceRetired: true}, nil
}

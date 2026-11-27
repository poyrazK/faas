package dbsched

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
)

type migrationVMM interface {
	LiveMigration(context.Context, Node, string, ImportSpec, bool) (*vmmdpb.DatabaseMigrationProgress, error)
	MigrationStatus(context.Context, Node, string) (*vmmdpb.DatabaseMigrationProgress, error)
}

func (s *Scheduler) LiveMigration(ctx context.Context, id string, spec ImportSpec, stop bool) (*vmmdpb.DatabaseMigrationProgress, error) {
	vmm, ok := s.vmm.(migrationVMM)
	if !ok {
		return nil, ErrImportUnsupported
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if vm.Replicated && !stop {
		return nil, fmt.Errorf("%w: logical migration requires a single-zone target", ErrConflict)
	}
	node, err := s.sourceInspectionNode(ctx, id, spec)
	if err != nil {
		return nil, err
	}
	p, err := vmm.LiveMigration(ctx, node, id, spec, stop)
	_ = s.Touch(ctx, id)
	return p, err
}

func (s *Scheduler) MigrationStatus(ctx context.Context, id string) (*vmmdpb.DatabaseMigrationProgress, error) {
	vmm, ok := s.vmm.(migrationVMM)
	if !ok {
		return nil, ErrImportUnsupported
	}
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	paused := &vmmdpb.DatabaseMigrationProgress{State: dbmigration.Paused, Reason: "target_not_running"}
	if vm.State != StateRunning {
		return paused, nil
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return nil, err
	}
	p, err := vmm.MigrationStatus(ctx, node, id)
	if errors.Is(err, ErrVMNotRunning) {
		return paused, nil
	}
	return p, err
}

func (s *Scheduler) PrepareMigration(ctx context.Context, id string, spec ImportSpec) (*vmmdpb.DatabaseMigrationProgress, error) {
	vmm, ok := s.vmm.(interface {
		PrepareMigration(context.Context, Node, string, ImportSpec) (*vmmdpb.DatabaseMigrationProgress, error)
	})
	if !ok {
		return nil, ErrImportUnsupported
	}
	node, err := s.sourceInspectionNode(ctx, id, spec)
	if err != nil {
		return nil, err
	}
	p, err := vmm.PrepareMigration(ctx, node, id, spec)
	_ = s.Touch(ctx, id)
	return p, err
}

func (s *Scheduler) VerifyMigration(ctx context.Context, id string, spec ImportSpec, verification string) (*vmmdpb.DatabaseMigrationProgress, error) {
	vmm, ok := s.vmm.(interface {
		VerifyMigration(context.Context, Node, string, ImportSpec, string) (*vmmdpb.DatabaseMigrationProgress, error)
	})
	if !ok {
		return nil, ErrImportUnsupported
	}
	node, err := s.sourceInspectionNode(ctx, id, spec)
	if err != nil {
		return nil, err
	}
	p, err := vmm.VerifyMigration(ctx, node, id, spec, verification)
	_ = s.Touch(ctx, id)
	return p, err
}

func (s *Scheduler) ControlMigrationActivation(ctx context.Context, id string, op dbguest.Op, a dbmigration.Activation) error {
	vmm, ok := s.vmm.(interface {
		ControlMigrationActivation(context.Context, Node, string, dbguest.Op, dbmigration.Activation) error
	})
	if !ok {
		return ErrImportUnsupported
	}
	vm, err := s.Wake(ctx, id)
	if err != nil {
		return err
	}
	if vm.Replicated {
		return ErrConflict
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return err
	}
	err = vmm.ControlMigrationActivation(ctx, node, id, op, a)
	_ = s.Touch(ctx, id)
	return err
}

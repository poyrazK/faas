package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type cloneTerminalCompensationWorkerStore interface {
	state.ProjectEnvironmentCloneTerminalCompensationStore
	CleanupProjectEnvironmentCloneConfigurationForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	CleanupProjectEnvironmentCloneWorkloadsForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	CleanupProjectEnvironmentCloneDeploymentsForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	CleanupProjectEnvironmentCloneEnvironmentForLease(context.Context, state.ProjectEnvironmentCloneLease) error
}

func cloneCoordinatorHasCommonCheckpoint(op state.ProjectEnvironmentCloneOperation) bool {
	for _, r := range op.Resources {
		switch r.Kind {
		case "data_checkpoint", "data_checkpoint_receipt", "data_checkpoint_adoption", "data_checkpoint_release":
			return true
		}
	}
	return false
}

// These metadata transitions consume existing native retirement proofs. The
// worker cannot manufacture those proofs or abandon their original inventory.
// Native retirement uses the existing durable claim/observation protocol only
// when private wiring explicitly supplies a qualified owner driver.
func (s *server) completeProjectEnvironmentCloneCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(cloneTerminalCompensationWorkerStore)
	if !ok || s.cloneWorkerAdmission == nil {
		return l, errCloneCompensationUnavailable
	}
	if s.cloneWorkloadRetirementDriver != nil {
		var err error
		l, err = s.retireCheckpointWorkloads(ctx, l, s.cloneWorkloadRetirementDriver)
		if err != nil {
			return l, err
		}
	}
	steps := []struct {
		name string
		run  func(context.Context, state.ProjectEnvironmentCloneLease) error
	}{
		{"configuration", store.CleanupProjectEnvironmentCloneConfigurationForLease},
		{"workloads", store.CleanupProjectEnvironmentCloneWorkloadsForLease},
		{"deployments", store.CleanupProjectEnvironmentCloneDeploymentsForLease},
		{"environment", store.CleanupProjectEnvironmentCloneEnvironmentForLease},
	}
	for _, step := range steps {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		if err := step.run(ctx, l); err != nil {
			return l, fmt.Errorf("cleanup clone %s: %w", step.name, err)
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	receipt, err := store.CompleteProjectEnvironmentCloneCheckpointCompensation(ctx, l)
	// Recover a lost commit response with the ORIGINAL token and revision. Do not
	// renew or release a lease which that transaction may already have cleared.
	if err != nil && ctx.Err() == nil && !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrInvalidArgument) {
		if admissionErr := s.cloneWorkerAdmission(ctx); admissionErr != nil {
			return l, admissionErr
		}
		receipt, err = store.CompleteProjectEnvironmentCloneCheckpointCompensation(ctx, l)
	}
	if err != nil {
		return l, err
	}
	if receipt.Report.OperationID != l.Operation.ID || receipt.Report.Revision != l.Operation.Revision {
		return l, state.ErrConflict
	}
	l.Operation.Status = state.CloneOperationCompensated
	l.Operation.Revision = receipt.Report.Revision + 1
	l.Token, l.ExpiresAt = "", time.Time{}
	return l, nil
}

package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectEnvironmentCloneWorkloadBorrowers reports references requiring their
// own retirement authority. Counts include cross-environment references and
// inconsistent environment/spec pairs. This report never authorizes cascades.
type ProjectEnvironmentCloneWorkloadBorrowers struct {
	OperationID, EnvironmentID, NativePlanSHA256, NativeObservationSHA256 string
	ForeignDeploymentPins, PromotionPreparedSpecs, PromotionPreviousSpecs int64
	QueueRuntimeSets, InvocationWorkAdmissions, InvocationQueueAdmissions int64
}

func (r ProjectEnvironmentCloneWorkloadBorrowers) blocked() bool {
	return r.ForeignDeploymentPins != 0 || r.PromotionPreparedSpecs != 0 || r.PromotionPreviousSpecs != 0 || r.QueueRuntimeSets != 0 || r.InvocationWorkAdmissions != 0 || r.InvocationQueueAdmissions != 0
}

// ProjectEnvironmentCloneWorkloadBorrowersForLease authenticates original
// workload ownership and native closure before reading the bounded inventory.
// It neither retires operational borrowers nor clears compensation blockers.
func (s *PgStore) ProjectEnvironmentCloneWorkloadBorrowersForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneWorkloadBorrowers, error) {
	var out ProjectEnvironmentCloneWorkloadBorrowers
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if native.State != "retired" || native.Plan.EnvironmentID == "" {
			return ErrConflict
		}
		out, err = readCloneWorkloadBorrowersTx(ctx, tx, op, native)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneWorkloadBorrowers{}, err
	}
	return out, nil
}

func readCloneWorkloadBorrowersTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, native ProjectEnvironmentCloneCheckpointWorkloadRetirement) (ProjectEnvironmentCloneWorkloadBorrowers, error) {
	row, err := sqlc.New().ReadCloneWorkloadConfigurationBorrowers(ctx, tx, sqlc.ReadCloneWorkloadConfigurationBorrowersParams{OperationID: mustPgUUID(op.ID), EnvironmentID: mustPgUUID(native.Plan.EnvironmentID)})
	if err != nil {
		return ProjectEnvironmentCloneWorkloadBorrowers{}, mapErr(err)
	}
	return ProjectEnvironmentCloneWorkloadBorrowers{OperationID: op.ID, EnvironmentID: native.Plan.EnvironmentID, NativePlanSHA256: cloneCheckpointSHA(native.Plan), NativeObservationSHA256: native.ObservationSHA256, ForeignDeploymentPins: row.ForeignDeploymentPins, PromotionPreparedSpecs: row.PromotionPreparedSpecs, PromotionPreviousSpecs: row.PromotionPreviousSpecs, QueueRuntimeSets: row.QueueRuntimeSets, InvocationWorkAdmissions: row.InvocationWorkAdmissions, InvocationQueueAdmissions: row.InvocationQueueAdmissions}, nil
}

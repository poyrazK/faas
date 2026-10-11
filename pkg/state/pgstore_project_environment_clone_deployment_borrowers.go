package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectEnvironmentCloneDeploymentBorrowers reports retained direct foreign-key
// references to originally copied deployments. Keys are constraint names, so two
// references from the same table remain distinguishable. Counts include inactive
// rows and inconsistent tenant metadata. They never authorize deletion.
type ProjectEnvironmentCloneDeploymentBorrowers struct {
	OperationID, NativePlanSHA256, NativeObservationSHA256 string
	Deployments                                            map[string]map[string]int64
}

// ProjectEnvironmentCloneDeploymentBorrowersForLease verifies native retirement
// and the original artifact/configuration proof chain before observing borrowers.
// Scope-only and soft references require separate closure before deletion.
func (s *PgStore) ProjectEnvironmentCloneDeploymentBorrowersForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneDeploymentBorrowers, error) {
	var out ProjectEnvironmentCloneDeploymentBorrowers
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if native.State != "retired" || native.Plan.EnvironmentID == "" {
			return ErrConflict
		}
		ownership, err := verifyCloneConfigurationOwnershipTx(ctx, tx, op, native.Plan.EnvironmentID)
		if err != nil {
			return err
		}
		if ownership == "" {
			return ErrConflict
		}
		out = ProjectEnvironmentCloneDeploymentBorrowers{OperationID: op.ID, NativePlanSHA256: cloneCheckpointSHA(native.Plan), NativeObservationSHA256: native.ObservationSHA256, Deployments: make(map[string]map[string]int64)}
		for _, w := range native.Plan.Workloads {
			if w.TargetDeploymentID == "" {
				continue
			}
			raw, err := sqlc.New().ReadCloneDeploymentBorrowers(ctx, tx, mustPgUUID(w.TargetDeploymentID))
			if err != nil {
				return mapErr(err)
			}
			counts := make(map[string]int64)
			if err = json.Unmarshal(raw, &counts); err != nil {
				return ErrConflict
			}
			out.Deployments[w.TargetDeploymentID] = counts
		}
		return nil
	})
	if err != nil {
		return ProjectEnvironmentCloneDeploymentBorrowers{}, err
	}
	return out, nil
}

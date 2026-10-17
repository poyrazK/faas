package state

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointCompensationReconciliationStore = (*PgStore)(nil)

// Reconcile authenticates the complete data-cleanup chain under the same clock,
// operation, app and worker locks as capture retirement. No resource label can
// substitute for a saved proof. Errors return no partial authenticated report.
func (s *PgStore) ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCompensationReconciliation, error) {
	var out ProjectEnvironmentCloneCheckpointCompensationReconciliation
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = reconcileCheckpointCompensationTx(ctx, tx, op)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointCompensationReconciliation{}, err
	}
	return out, nil
}

// Call only after the configuration clock and owned operation/app locks. Both
// borrower fences precede evidence reads and stay held until the caller commits.
// Keeping verification in this transaction allows a future terminal writer to
// consume the report without opening a second transaction or a check/write gap.
func reconcileCheckpointCompensationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointCompensationReconciliation, error) {
	var out ProjectEnvironmentCloneCheckpointCompensationReconciliation
	q := sqlc.New()
	if err := q.LockCloneDeploymentCleanupBorrowerWriters(ctx, tx); err != nil {
		return out, mapErr(err)
	}
	if err := q.LockCloneEnvironmentCleanupScopeBorrowers(ctx, tx); err != nil {
		return out, mapErr(err)
	}
	if err := q.LockCloneBindingEnvironmentCleanupBorrowers(ctx, tx); err != nil {
		return out, mapErr(err)
	}
	in, err := readCheckpointCaptureRetirementTx(ctx, tx, op)
	if err != nil {
		return out, checkpointPgCompensationDependencyError(err)
	}
	if in.State != "retired" {
		return out, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return out, err
	}
	out = checkpointCompensationReconciliation(op, in, records)
	if err = reconcileCheckpointWorkloadCleanupTx(ctx, tx, op, records, &out); err != nil {
		return ProjectEnvironmentCloneCheckpointCompensationReconciliation{}, err
	}
	sortCheckpointCompensationBlockers(&out)
	return out, nil
}

func checkpointCompensationReconciliation(op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointCaptureRetirement, records []projectCloneWorkloadRecord) ProjectEnvironmentCloneCheckpointCompensationReconciliation {
	out := ProjectEnvironmentCloneCheckpointCompensationReconciliation{
		OperationID: op.ID, Revision: op.Revision, ResourcesSHA256: cloneCheckpointSHA(op.Resources), SourceRevisionHash: op.SourceRevisionHash,
		SourceCompensationSHA256: in.Plan.SourceCompensation.ObservationSHA256, CaptureRetirementSHA256: in.ObservationSHA256,
		Blockers: []ProjectEnvironmentCloneCheckpointCompensationBlocker{},
	}
	// Data retirement authenticates the original capture and its borrowers, but
	// cannot prove workload runtime or copied settings/values/policies withdrawn.
	// Use frozen records too: an omitted resource label cannot hide that scope.
	for _, record := range records {
		out.Blockers = append(out.Blockers, ProjectEnvironmentCloneCheckpointCompensationBlocker{
			Kind: "workload", Name: record.AppID, TargetID: record.TargetDeploymentID, Reason: "workload_configuration_retirement_unverified",
		})
	}
	for _, resource := range op.Resources {
		if !checkpointCompensationDataResource(op, in.Plan.SourceCompensation.Plan.Capture, resource) {
			out.Blockers = append(out.Blockers, ProjectEnvironmentCloneCheckpointCompensationBlocker{
				Kind: resource.Kind, Name: resource.Name, TargetID: resource.TargetID, Reason: "resource_retirement_unverified",
			})
		}
	}
	if op.TargetReleaseSetID != "" {
		out.Blockers = append(out.Blockers, ProjectEnvironmentCloneCheckpointCompensationBlocker{
			Kind: "release_set", Name: op.TargetEnvironment, TargetID: op.TargetReleaseSetID, Reason: "release_withdrawal_unverified",
		})
	}
	sortCheckpointCompensationBlockers(&out)
	return out
}

func checkpointCompensationDataResource(op ProjectEnvironmentCloneOperation, capture ProjectEnvironmentCloneCheckpointPlan, resource ProjectEnvironmentCloneResource) bool {
	switch resource.Kind {
	case "data_checkpoint", "data_checkpoint_receipt", "data_checkpoint_adoption", "data_checkpoint_release":
		return resource.Name == op.SourceEnvironment
	case "managed_postgres", "object_storage":
		for _, source := range capture.Sources {
			if source.Kind == resource.Kind && source.ID == resource.SourceID && resource.Name == source.ID {
				return true
			}
		}
	}
	return false
}

func sortCheckpointCompensationBlockers(out *ProjectEnvironmentCloneCheckpointCompensationReconciliation) {
	sort.Slice(out.Blockers, func(i, j int) bool {
		a, b := out.Blockers[i], out.Blockers[j]
		return a.Kind+"\x00"+a.Name+"\x00"+a.TargetID+"\x00"+a.Reason < b.Kind+"\x00"+b.Name+"\x00"+b.TargetID+"\x00"+b.Reason
	})
}

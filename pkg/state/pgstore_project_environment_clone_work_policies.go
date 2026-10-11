package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func captureCloneWorkPoliciesTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, appID, scope string) (ProjectEnvironmentCloneWorkPolicyDefinitions, error) {
	row, err := new(sqlc.Queries).CaptureProjectEnvironmentCloneWorkPolicies(ctx, tx, sqlc.CaptureProjectEnvironmentCloneWorkPoliciesParams{
		AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(appID), SourceScope: scope})
	if err != nil {
		return ProjectEnvironmentCloneWorkPolicyDefinitions{}, mapErr(err)
	}
	if row.OwnershipViolations != 0 {
		return ProjectEnvironmentCloneWorkPolicyDefinitions{}, ErrConflict
	}
	var definitions ProjectEnvironmentCloneWorkPolicyDefinitions
	if err := json.Unmarshal(row.Definitions, &definitions); err != nil {
		return definitions, ErrConflict
	}
	if definitions.AppID != appID || definitions.SourceScope != scope {
		return definitions, ErrConflict
	}
	return normalizeCloneWorkPolicyDefinitions(definitions)
}

// Workers consume the committed catalogue under their current lease. Missing
// definitions never fall back to live production policy/binding lookups.
func (s *PgStore) ProjectEnvironmentCloneWorkPoliciesForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) ([]ProjectEnvironmentCloneWorkPolicyCapture, error) {
	if !validCloneLeaseIdentity(lease) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return nil, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return nil, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return nil, err
	}
	root, err := verifyCloneConfigurationCaptureDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, records)
	if err != nil {
		return nil, err
	}
	if root.Version != 1 {
		return nil, ErrProjectEnvironmentClonePolicyCaptureUnavailable
	}
	captures := make([]ProjectEnvironmentCloneWorkPolicyCapture, 0, len(records))
	for _, record := range records {
		if record.snapshot.Policies == nil || record.snapshot.Policies.Work == nil {
			return nil, ErrProjectEnvironmentClonePolicyCaptureUnavailable
		}
		definitions, err := normalizeCloneWorkPolicyDefinitions(*record.snapshot.Policies.Work)
		if err != nil || definitions.AppID != record.AppID || definitions.SourceScope != record.SourceScope {
			return nil, ErrConflict
		}
		hash, err := cloneWorkPolicyDefinitionsHash(definitions)
		if err != nil {
			return nil, err
		}
		captures = append(captures, ProjectEnvironmentCloneWorkPolicyCapture{OperationID: op.ID, Hash: hash, Definitions: definitions})
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return nil, err
	}
	return captures, tx.Commit(ctx)
}

var _ ProjectEnvironmentCloneWorkPolicyCaptureStore = (*PgStore)(nil)

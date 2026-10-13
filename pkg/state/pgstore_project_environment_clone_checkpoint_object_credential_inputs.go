package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectCredentialInputStore = (*PgStore)(nil)

// Authenticate the complete original graph under the capturing lease and row
// locks. No current source catalogue, secret opening, grant issuance or target
// publication is performed. Callers must claim durable issuance authority later.
func (s *PgStore) ProjectEnvironmentCloneCheckpointObjectCredentialInputs(ctx context.Context, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialInputs, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialInputs
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = checkpointObjectCredentialInputsTx(ctx, tx, op, l, retention)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	return out, nil
}

func checkpointObjectCredentialInputsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialInputs, error) {
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	if release.State != "released" {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, ErrConflict
	}
	if err = validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, release, false); err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	if err = validateCloneCheckpointReleaseObservation(release, retention, l.ExpiresAt); err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	proofs := []ProjectEnvironmentCloneCheckpointObjectVerification{}
	for _, v := range views {
		for _, b := range v.Buckets {
			p, err := readCheckpointObjectVerificationTx(ctx, tx, op, b.ID)
			if err != nil {
				return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
			}
			if p.State != "verified" {
				return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, ErrConflict
			}
			if err = validateCheckpointObjectVerificationTx(ctx, tx, op, l, p, &retention); err != nil {
				return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
			}
			row, err := sqlc.New().ReadProjectEnvironmentCloneObjectBucket(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectBucketParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(v.AppID), ID: mustPgUUID(p.Plan.Target.TargetBucketID), EnvironmentCloneOperationID: mustPgUUID(op.ID)})
			if err != nil {
				return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, mapErr(err)
			}
			bucket := objectBucketFromSQL(row)
			if bucket.Name != ProjectEnvironmentCloneObjectBucketName(op, v.AppID, b.ID) || bucket.Scope != op.TargetEnvironment {
				return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, ErrConflict
			}
			proofs = append(proofs, p)
		}
	}
	return checkpointObjectCredentialInputs(op, views, proofs)
}

package state

import (
	"context"

	"github.com/jackc/pgx/v5"
)

var _ ProjectEnvironmentCloneCheckpointObjectCredentialInputStore = (*PgStore)(nil)

// Authenticate the complete original graph under the capturing lease and row
// locks. No current source catalogue, secret opening, grant issuance or target
// publication is performed. Callers must claim durable issuance authority later.
func (s *PgStore) ProjectEnvironmentCloneCheckpointObjectCredentialInputs(ctx context.Context, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialInputs, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialInputs
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if release.State != "released" {
			return ErrConflict
		}
		if err = validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, release, false); err != nil {
			return err
		}
		if err = validateCloneCheckpointReleaseObservation(release, retention, l.ExpiresAt); err != nil {
			return err
		}
		records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
		if err != nil {
			return err
		}
		views, err := cloneBindingViews(records)
		if err != nil {
			return err
		}
		proofs := []ProjectEnvironmentCloneCheckpointObjectVerification{}
		for _, v := range views {
			for _, b := range v.Buckets {
				p, err := readCheckpointObjectVerificationTx(ctx, tx, op, b.ID)
				if err != nil {
					return err
				}
				if p.State != "verified" {
					return ErrConflict
				}
				if err = validateCheckpointObjectVerificationTx(ctx, tx, op, l, p, &retention); err != nil {
					return err
				}
				proofs = append(proofs, p)
			}
		}
		out, err = checkpointObjectCredentialInputs(op, views, proofs)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialInputs{}, err
	}
	return out, nil
}

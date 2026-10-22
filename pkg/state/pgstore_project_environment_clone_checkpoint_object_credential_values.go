package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectCredentialValueStore = (*PgStore)(nil)

func readCheckpointObjectValuesTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, r ProjectEnvironmentCloneCheckpointObjectCredentialReservation) (ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialValues
	row, err := sqlc.New().ReadCloneCheckpointObjectValues(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return out, mapErr(err)
	}
	if row.State != "sealed" || json.Unmarshal(row.Payload, &out) != nil {
		return out, ErrConflict
	}
	out.SHA256 = row.PayloadSha256
	if err = validateCheckpointObjectCredentialValues(out, r); err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialValues{}, err
	}
	return out, nil
}
func (s *PgStore) ProjectEnvironmentCloneCheckpointObjectCredentialValues(ctx context.Context, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialValues
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		r, err := checkpointObjectCredentialReservationTx(ctx, tx, op, l, retention, false)
		if err != nil {
			return err
		}
		out, err = readCheckpointObjectValuesTx(ctx, tx, op, r)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialValues{}, err
	}
	return out, nil
}
func (s *PgStore) PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(ctx context.Context, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointObjectCredentialValues, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialValues
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		r, err := checkpointObjectCredentialReservationTx(ctx, tx, op, l, retention, false)
		if err != nil {
			return err
		}
		if in.OperationID != op.ID || in.ReservationSHA256 != r.SHA256 {
			return ErrConflict
		}
		out, err = readCheckpointObjectValuesTx(ctx, tx, op, r)
		if err == nil {
			return nil
		} // First committed envelope wins concurrent proposals.
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err = validateCheckpointObjectCredentialValues(in, r); err != nil {
			return err
		}
		encoded, err := json.Marshal(in)
		if err != nil {
			return err
		}
		_, err = sqlc.New().InsertCloneCheckpointObjectValues(ctx, tx, sqlc.InsertCloneCheckpointObjectValuesParams{OperationID: mustPgUUID(op.ID), Payload: encoded, PayloadSha256: in.SHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = readCheckpointObjectValuesTx(ctx, tx, op, r)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialValues{}, err
	}
	return out, nil
}

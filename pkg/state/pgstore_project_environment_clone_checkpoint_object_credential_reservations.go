package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectCredentialReservationStore = (*PgStore)(nil)

func checkpointObjectCredentialReservationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointObjectCredentialID) (ProjectEnvironmentCloneCheckpointObjectCredentialReservation, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialReservation
	if r.State != "planned" || json.Unmarshal(r.Plan, &out) != nil || out.Inputs.OperationID != pgUUIDString(r.OperationID) {
		return out, ErrConflict
	}
	out.Inputs.SHA256 = cloneCheckpointSHA(out.Inputs)
	out.SHA256 = r.PlanSha256
	return out, nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointObjectCredentials(ctx context.Context, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCredentialReservation, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialReservation
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		inputs, err := checkpointObjectCredentialInputsTx(ctx, tx, op, l, retention)
		if err != nil {
			return err
		}
		q := sqlc.New()
		r, err := q.ReadCloneCheckpointObjectCredentialReservation(ctx, tx, mustPgUUID(op.ID))
		if err != nil {
			if !errors.Is(mapErr(err), ErrNotFound) {
				return mapErr(err)
			}
			out, err = newCheckpointObjectCredentialReservation(inputs)
			if err != nil {
				return err
			}
			encoded, marshalErr := json.Marshal(out)
			if marshalErr != nil {
				return marshalErr
			}
			r, err = q.InsertCloneCheckpointObjectCredentialReservation(ctx, tx, sqlc.InsertCloneCheckpointObjectCredentialReservationParams{OperationID: mustPgUUID(op.ID), Plan: encoded, PlanSha256: out.SHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
			if err != nil {
				return mapErr(err)
			}
		}
		out, err = checkpointObjectCredentialReservationFromSQL(r)
		if err != nil {
			return err
		}
		if err = validateCheckpointObjectCredentialReservation(out, inputs); err != nil {
			return err
		}
		// Existing credential rows are not ownership evidence for this reservation.
		// Issuance will need a separate committed claim protocol before creating them.
		for _, m := range out.Members {
			exists, err := q.CloneCheckpointObjectCredentialIdentityExists(ctx, tx, mustPgUUID(m.TargetCredentialID))
			if err != nil {
				return mapErr(err)
			}
			if exists {
				return ErrConflict
			}
		}
		return nil
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
	}
	return out, nil
}

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
		var err error
		out, err = checkpointObjectCredentialReservationTx(ctx, tx, op, l, retention, true)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
	}
	return out, nil
}

func checkpointObjectCredentialReservationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation, create bool) (ProjectEnvironmentCloneCheckpointObjectCredentialReservation, error) {
	var out ProjectEnvironmentCloneCheckpointObjectCredentialReservation
	inputs, err := checkpointObjectCredentialInputsTx(ctx, tx, op, l, retention)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
	}
	q := sqlc.New()
	r, err := q.ReadCloneCheckpointObjectCredentialReservation(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		if !errors.Is(mapErr(err), ErrNotFound) {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, mapErr(err)
		}
		if !create {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, ErrNotFound
		}
		out, err = newCheckpointObjectCredentialReservation(inputs)
		if err != nil {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
		}
		encoded, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, marshalErr
		}
		r, err = q.InsertCloneCheckpointObjectCredentialReservation(ctx, tx, sqlc.InsertCloneCheckpointObjectCredentialReservationParams{OperationID: mustPgUUID(op.ID), Plan: encoded, PlanSha256: out.SHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, mapErr(err)
		}
	}
	out, err = checkpointObjectCredentialReservationFromSQL(r)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
	}
	if err = validateCheckpointObjectCredentialReservation(out, inputs); err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
	}
	// Only an authenticated publication of the entire matrix can own existing
	// credential identities; an unrelated or revoked row still conflicts.
	owned, err := checkpointObjectPublicationOwnedTx(ctx, tx, op)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, err
	}
	for _, m := range out.Members {
		exists, err := q.CloneCheckpointObjectCredentialIdentityExists(ctx, tx, mustPgUUID(m.TargetCredentialID))
		if err != nil {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, mapErr(err)
		}
		if exists && !owned {
			return ProjectEnvironmentCloneCheckpointObjectCredentialReservation{}, ErrConflict
		}
	}
	return out, nil
}

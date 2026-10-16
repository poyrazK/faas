package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPostgresVerificationStore = (*PgStore)(nil)

func checkpointPostgresVerificationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPostgresVerification) (ProjectEnvironmentCloneCheckpointPostgresVerification, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresVerification{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Isolation.OperationID != pgUUIDString(r.OperationID) || in.Plan.Isolation.SourceDatabaseID != pgUUIDString(r.SourceDatabaseID) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointPostgresVerificationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresVerification, error) {
	r, err := sqlc.New().ReadCloneCheckpointPostgresVerification(ctx, tx, sqlc.ReadCloneCheckpointPostgresVerificationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresVerification{}, mapErr(err)
	}
	return checkpointPostgresVerificationFromSQL(r)
}
func checkpointPostgresVerificationPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id string, closed *ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresVerificationPlan, error) {
	var p ProjectEnvironmentCloneCheckpointPostgresVerificationPlan
	isolation, err := readCheckpointPostgresIsolationTx(ctx, tx, op, id)
	if err != nil {
		return p, err
	}
	if isolation.State != "adopted" {
		return p, ErrConflict
	}
	if _, err = validateCheckpointPostgresIsolationTx(ctx, tx, op, l, isolation, retention); err != nil {
		return p, err
	}
	if closed != nil {
		if err = validateCheckpointPostgresIsolationObservation(isolation, *closed, l.ExpiresAt); err != nil {
			return p, err
		}
	}
	return ProjectEnvironmentCloneCheckpointPostgresVerificationPlan{Isolation: isolation.Plan, IsolationSHA256: isolation.ObservationSHA256, MaxReadBytesPerSide: api.PostgresCopyArchiveMaxBytes}, nil
}
func validateCheckpointPostgresVerificationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointPostgresVerification, closed *ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	p, err := checkpointPostgresVerificationPlanTx(ctx, tx, op, l, in.Plan.Isolation.SourceDatabaseID, closed, retention)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p, in.Plan) {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresVerification, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresVerification
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresVerificationTx(ctx, tx, op, id)
		if err == nil {
			return validateCheckpointPostgresVerificationTx(ctx, tx, op, l, out, nil, nil)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointPostgresVerificationPlanTx(ctx, tx, op, l, id, nil, nil)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		r, err := sqlc.New().InsertCloneCheckpointPostgresVerification(ctx, tx, sqlc.InsertCloneCheckpointPostgresVerificationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Plan: encoded, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresVerificationFromSQL(r)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string, closed ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresVerification, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresVerification
	dispatch := false
	if !validCloneCredentialSourceID(id) {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresVerificationTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresVerificationTx(ctx, tx, op, l, out, &closed, &retention); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		r, err := sqlc.New().ClaimCloneCheckpointPostgresVerification(ctx, tx, sqlc.ClaimCloneCheckpointPostgresVerificationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresVerificationFromSQL(r)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, closed ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	return s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointPostgresVerificationTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresVerificationTx(ctx, tx, op, l, in, &closed, &retention); err != nil {
			return err
		}
		if err = validateCheckpointPostgresVerificationObservation(in, o); err != nil {
			return err
		}
		if in.State == "verified" {
			return nil
		}
		n, err := sqlc.New().FinishCloneCheckpointPostgresVerification(ctx, tx, sqlc.FinishCloneCheckpointPostgresVerificationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ObservationSha256: cloneCheckpointSHA(o), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

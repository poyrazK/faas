package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPostgresRuntimeStore = (*PgStore)(nil)

func checkpointPostgresRuntimeFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPostgresRuntime) (ProjectEnvironmentCloneCheckpointPostgresRuntime, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresRuntime{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Verification.Isolation.OperationID != pgUUIDString(r.OperationID) || in.Plan.Verification.Isolation.SourceDatabaseID != pgUUIDString(r.SourceDatabaseID) {
		return in, ErrConflict
	}
	if in.State == "ready" {
		if json.Unmarshal(r.Observation, &in.Observation) != nil || in.Observation.DataResourceID != r.DataResourceID.String || validateCheckpointPostgresRuntimeObservation(in, in.Observation, time.Time{}) != nil {
			return in, ErrConflict
		}
	}
	return in, nil
}
func readCheckpointPostgresRuntimeTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresRuntime, error) {
	r, err := sqlc.New().ReadCloneCheckpointPostgresRuntime(ctx, tx, sqlc.ReadCloneCheckpointPostgresRuntimeParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresRuntime{}, mapErr(err)
	}
	return checkpointPostgresRuntimeFromSQL(r)
}
func checkpointPostgresRuntimePlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id string, verified *ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, closed *ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresRuntimePlan, error) {
	var p ProjectEnvironmentCloneCheckpointPostgresRuntimePlan
	verification, err := readCheckpointPostgresVerificationTx(ctx, tx, op, id)
	if err != nil {
		return p, err
	}
	if verification.State != "verified" {
		return p, ErrConflict
	}
	if err = validateCheckpointPostgresVerificationTx(ctx, tx, op, l, verification, closed, retention); err != nil {
		return p, err
	}
	if verified != nil {
		if err = validateCheckpointPostgresVerificationObservation(verification, *verified); err != nil {
			return p, err
		}
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return p, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return p, err
	}
	source, _, _, err := capturedCloneDatabaseReservation(op, views, id)
	if err != nil {
		return p, err
	}
	return ProjectEnvironmentCloneCheckpointPostgresRuntimePlan{Verification: verification.Plan, VerificationSHA256: verification.ObservationSHA256, Spec: checkpointPostgresRuntimeSpec(source)}, nil
}
func validateCheckpointPostgresRuntimeTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointPostgresRuntime, verified *ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, closed *ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	p, err := checkpointPostgresRuntimePlanTx(ctx, tx, op, l, in.Plan.Verification.Isolation.SourceDatabaseID, verified, closed, retention)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p, in.Plan) {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresRuntime, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresRuntime
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresRuntimeTx(ctx, tx, op, id)
		if err == nil {
			return validateCheckpointPostgresRuntimeTx(ctx, tx, op, l, out, nil, nil, nil)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointPostgresRuntimePlanTx(ctx, tx, op, l, id, nil, nil, nil)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		r, err := sqlc.New().InsertCloneCheckpointPostgresRuntime(ctx, tx, sqlc.InsertCloneCheckpointPostgresRuntimeParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Plan: encoded, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresRuntimeFromSQL(r)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(ctx context.Context, l ProjectEnvironmentCloneLease, id string, verified ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, closed ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresRuntime, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresRuntime
	dispatch := false
	if !validCloneCredentialSourceID(id) {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresRuntimeTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresRuntimeTx(ctx, tx, op, l, out, &verified, &closed, &retention); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		r, err := sqlc.New().ClaimCloneCheckpointPostgresRuntime(ctx, tx, sqlc.ClaimCloneCheckpointPostgresRuntimeParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresRuntimeFromSQL(r)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresRuntime(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, verified ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, closed ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneDatabaseTarget, error) {
	var target ProjectEnvironmentCloneDatabaseTarget
	if !validCloneCredentialSourceID(id) {
		return target, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointPostgresRuntimeTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresRuntimeTx(ctx, tx, op, l, in, &verified, &closed, &retention); err != nil {
			return err
		}
		if err = validateCheckpointPostgresRuntimeObservation(in, o, l.ExpiresAt); err != nil {
			return err
		}
		target = ProjectEnvironmentCloneDatabaseTarget{ID: in.Plan.Verification.Isolation.OwnerID, State: "ready"}
		if in.State == "ready" {
			return nil
		}
		stable := o
		stable.ReadyThrough = time.Time{}
		encoded, err := json.Marshal(stable)
		if err != nil {
			return err
		}
		q := sqlc.New()
		n, err := q.FinishCloneCheckpointPostgresRuntime(ctx, tx, sqlc.FinishCloneCheckpointPostgresRuntimeParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Observation: encoded, ObservationSha256: checkpointPostgresRuntimeSHA(o), DataResourceID: o.DataResourceID, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.FinishCloneCheckpointPostgresRuntimeDatabase(ctx, tx, sqlc.FinishCloneCheckpointPostgresRuntimeDatabaseParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
	return target, err
}

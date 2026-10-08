package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"reflect"
)

var _ ProjectEnvironmentCloneCheckpointPostgresIsolationStore = (*PgStore)(nil)

func checkpointPostgresIsolationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPostgresIsolation) (ProjectEnvironmentCloneCheckpointPostgresIsolation, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresIsolation{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.OperationID != pgUUIDString(r.OperationID) || in.Plan.SourceDatabaseID != pgUUIDString(r.SourceDatabaseID) || in.State == "adopted" && in.Plan.OwnerID != pgUUIDString(r.AdoptedDatabaseID) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointPostgresIsolationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresIsolation, error) {
	r, err := sqlc.New().ReadCloneCheckpointPostgresIsolation(ctx, tx, sqlc.ReadCloneCheckpointPostgresIsolationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresIsolation{}, mapErr(err)
	}
	return checkpointPostgresIsolationFromSQL(r)
}
func checkpointPostgresIsolationPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, lease ProjectEnvironmentCloneLease, id string, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresIsolationPlan, ProjectEnvironmentClonePostgresSnapshot, ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	var plan ProjectEnvironmentCloneCheckpointPostgresIsolationPlan
	var snapshot ProjectEnvironmentClonePostgresSnapshot
	var fork ProjectEnvironmentClonePostgresSnapshotRestore
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return plan, snapshot, fork, err
	}
	if release.State != "released" {
		return plan, snapshot, fork, ErrConflict
	}
	if err := validateCloneCheckpointReleaseJournalsTx(ctx, tx, op, release, false); err != nil {
		return plan, snapshot, fork, err
	}
	if retention != nil {
		if err := validateCloneCheckpointReleaseObservation(release, *retention, lease.ExpiresAt); err != nil {
			return plan, snapshot, fork, err
		}
	}
	_, snapshot, err = cloneSnapshotRestoreContextTx(ctx, tx, lease, id, false)
	if err != nil {
		return plan, snapshot, fork, err
	}
	fork, err = readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return plan, snapshot, fork, err
	}
	if fork.State != "restored" && fork.State != "adopted" || fork.TargetProviderResourceID == "" || fork.RestoredAt.IsZero() {
		return plan, snapshot, fork, ErrConflict
	}
	plan = ProjectEnvironmentCloneCheckpointPostgresIsolationPlan{OperationID: op.ID, AccountID: op.AccountID, SourceDatabaseID: id, OwnerID: fork.TargetOwnerID, NativeResourceID: fork.TargetProviderResourceID, BackendID: fork.BackendID, BackendFingerprint: fork.BackendFingerprint, CheckpointID: release.Receipt.CheckpointID, ProviderSnapshotID: snapshot.ProviderSnapshotID, SourceDataResourceID: snapshot.SourceDataResourceID, CapturePoint: snapshot.CapturePoint.UTC(), SnapshotCreatedAt: snapshot.SnapshotCreatedAt.UTC(), TargetCreatedAt: fork.TargetCreatedAt.UTC()}
	return plan, snapshot, fork, nil
}
func validateCheckpointPostgresIsolationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, lease ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointPostgresIsolation, retention *ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentClonePostgresSnapshot, error) {
	plan, snapshot, fork, err := checkpointPostgresIsolationPlanTx(ctx, tx, op, lease, in.Plan.SourceDatabaseID, retention)
	if err != nil {
		return snapshot, err
	}
	if !reflect.DeepEqual(plan, in.Plan) || (in.State == "adopted") != (fork.State == "adopted") {
		return snapshot, ErrConflict
	}
	return snapshot, nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresIsolation(ctx context.Context, lease ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresIsolation, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresIsolation
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresIsolationTx(ctx, tx, op, id)
		if err == nil {
			_, err = validateCheckpointPostgresIsolationTx(ctx, tx, op, lease, out, nil)
			return err
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		plan, _, fork, err := checkpointPostgresIsolationPlanTx(ctx, tx, op, lease, id, nil)
		if err != nil {
			return err
		}
		if fork.State != "restored" {
			return ErrConflict
		}
		encoded, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		row, err := sqlc.New().InsertCloneCheckpointPostgresIsolation(ctx, tx, sqlc.InsertCloneCheckpointPostgresIsolationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Plan: encoded, ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresIsolationFromSQL(row)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(ctx context.Context, lease ProjectEnvironmentCloneLease, id string, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresIsolation, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresIsolation
	dispatch := false
	if !validCloneCredentialSourceID(id) {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresIsolationTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if _, err = validateCheckpointPostgresIsolationTx(ctx, tx, op, lease, out, &retention); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		row, err := sqlc.New().ClaimCloneCheckpointPostgresIsolation(ctx, tx, sqlc.ClaimCloneCheckpointPostgresIsolationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresIsolationFromSQL(row)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresIsolation(ctx context.Context, lease ProjectEnvironmentCloneLease, id string, observed ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneDatabaseTarget, error) {
	var target ProjectEnvironmentCloneDatabaseTarget
	if !validCloneCredentialSourceID(id) {
		return target, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, lease, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		q := sqlc.New()
		if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
			return mapErr(err)
		}
		in, err := readCheckpointPostgresIsolationTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		snapshot, err := validateCheckpointPostgresIsolationTx(ctx, tx, op, lease, in, &retention)
		if err != nil {
			return err
		}
		if err := validateCheckpointPostgresIsolationObservation(in, observed, lease.ExpiresAt); err != nil {
			return err
		}
		if in.State == "adopted" {
			database, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(in.Plan.OwnerID)})
			if err != nil {
				return mapErr(err)
			}
			target = ProjectEnvironmentCloneDatabaseTarget{ID: in.Plan.OwnerID, State: database.State}
			return nil
		}
		target, _, err = adoptCloneSnapshotRestoreTx(ctx, tx, op, snapshot, lease)
		if err != nil {
			return err
		}
		n, err := q.FinishCloneCheckpointPostgresIsolation(ctx, tx, sqlc.FinishCloneCheckpointPostgresIsolationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ObservationSha256: checkpointPostgresIsolationSHA(observed), AdoptedDatabaseID: mustPgUUID(target.ID), ExpectedRevision: lease.Operation.Revision, WorkerToken: mustPgUUID(lease.Token)})
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

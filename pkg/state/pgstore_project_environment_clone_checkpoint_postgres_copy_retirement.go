package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPostgresCopyRetirementStore = (*PgStore)(nil)

func checkpointPgCopyRetirementFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPgCopyRetirement) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresCopyRetirement{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if len(r.Plan) > api.PostgresCheckpointCopyRetirementMaxBytes || json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Scope.OperationID != pgUUIDString(r.OperationID) || in.Plan.Scope.SourceDatabaseID != pgUUIDString(r.SourceDatabaseID) || validateCheckpointPostgresCopyRetirementPlan(in.Plan) != nil || !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) {
		return in, ErrConflict
	}
	if (in.State != "planned") != r.RequestedAt.Valid || (in.State == "retired") != r.RetiredAt.Valid || (in.State == "retired") != r.ObservationSha256.Valid || (in.State == "retired") != (len(r.Observation) > 0) {
		return in, ErrConflict
	}
	if in.State == "retired" && (len(r.Observation) > api.PostgresCheckpointCopyRetirementMaxBytes || json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointPostgresCopyRetirementObservation(in, in.Observation) != nil || !in.Observation.RetiredAt.Equal(r.RetiredAt.Time)) {
		return in, ErrConflict
	}
	return in, nil
}

// This authenticated reconstruction never consults current source configuration
// or provider health. Only exact terminal catalogue mutations are normalized.
func checkpointPgCopyRetirementPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string, saved *ProjectEnvironmentCloneCheckpointPostgresCopyRetirement) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan, error) {
	var p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan
	sourceProof, err := readCheckpointSourceCompensationTx(ctx, tx, op)
	if err != nil {
		return p, checkpointPgCompensationDependencyError(err)
	}
	if sourceProof.State != "compensated" || saved != nil && saved.State != "planned" && saved.RequestedAt.Before(sourceProof.Observation.CompensatedAt) {
		return p, ErrConflict
	}
	p.SourceCompensationSHA256 = sourceProof.ObservationSHA256
	snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, id)
	if err != nil {
		return p, checkpointPgCompensationDependencyError(err)
	}
	p.Capture, err = checkpointPgTargetCompensationOriginalPlanTx(ctx, tx, op, id, snapshot)
	if err != nil {
		return p, checkpointPgCompensationDependencyError(err)
	}
	q := sqlc.New()
	if p.Capture.Restore.State != "adopted" || p.Capture.Snapshot.State != "retained" {
		if saved == nil || saved.State != "retired" {
			return p, ErrConflict
		}
		row, e := q.ReadCloneCheckpointPgTargetCompensation(ctx, tx, sqlc.ReadCloneCheckpointPgTargetCompensationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
		if e != nil {
			return p, ErrConflict
		}
		proof, e := checkpointPgTargetCompensationFromSQL(row)
		if e != nil || proof.State != "compensated" || checkpointPgTargetCompensationMatchesTx(ctx, tx, op, proof, p.Capture) != nil {
			return p, ErrConflict
		}
		p.Capture.Restore.State = saved.Plan.Capture.Restore.State
		p.Capture.Restore.DeletionStartedAt = saved.Plan.Capture.Restore.DeletionStartedAt
		p.Capture.Restore.DeletedAt = saved.Plan.Capture.Restore.DeletedAt
		p.Capture.Snapshot.State = saved.Plan.Capture.Snapshot.State
		p.Capture.Snapshot.CleanupObservedAt = saved.Plan.Capture.Snapshot.CleanupObservedAt
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return p, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return p, err
	}
	source, resource, point, err := capturedCloneDatabaseReservation(op, views, id)
	if err != nil {
		return p, err
	}
	s, r := p.Capture.Snapshot, p.Capture.Restore
	p.Scope = copyinventory.Scope{PostgresMajor: source.PostgresMajor, ProjectID: op.ProjectID, OperationID: op.ID, AccountID: op.AccountID, SourceDatabaseID: id, CaptureDatabaseID: r.AdoptedDatabaseID, SourceVersion: s.SourceVersion, BackendID: s.BackendID, BackendFingerprint: s.BackendFingerprint, SourceProviderResourceID: s.SourceProviderResourceID, SourceDataResourceID: s.SourceDataResourceID, ProviderSnapshotID: s.ProviderSnapshotID, CaptureProviderResourceID: r.TargetProviderResourceID, CapturePoint: s.CapturePoint, SnapshotCreatedAt: s.SnapshotCreatedAt, CaptureCreatedAt: r.TargetCreatedAt}
	reader, err := readCloneCopyReaderTx(ctx, tx, p.Scope)
	if err == nil {
		canonicalCheckpointCopyReader(&reader)
		p.Reader = &reader
		row, e := q.ReadProjectEnvironmentClonePostgresCopyReader(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresCopyReaderParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
		if e != nil {
			return p, mapErr(e)
		}
		if saved != nil && saved.State == "retired" {
			if saved.Plan.Reader == nil || reader.State != "retired" || row.CheckpointRetirementObservationSha256.String != saved.ObservationSHA256 || !reader.RetiredAt.Equal(saved.Observation.RetiredAt) || reader.Available {
				return p, ErrConflict
			}
			expectedCleanup := saved.Plan.Reader.CleanupRequestedAt
			if expectedCleanup.IsZero() {
				expectedCleanup = saved.RequestedAt
			}
			if !reader.CleanupRequestedAt.Equal(expectedCleanup) {
				return p, ErrConflict
			}
			reader.State, reader.Available, reader.RetiredAt, reader.CleanupRequestedAt = saved.Plan.Reader.State, saved.Plan.Reader.Available, saved.Plan.Reader.RetiredAt, saved.Plan.Reader.CleanupRequestedAt
		} else if row.CheckpointRetirementObservationSha256.Valid {
			return p, ErrConflict
		}
	} else if !errors.Is(err, ErrNotFound) {
		return p, err
	}
	targetRow, err := q.ReadProjectEnvironmentClonePostgresCopyTarget(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresCopyTargetParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err == nil {
		target := clonePostgresCopyTargetFromSQL(targetRow)
		canonicalCheckpointCopyTarget(&target)
		p.Target = &target
		if !targetRow.CreatedAt.Valid || targetRow.CreatedAt.Time.Before(p.Scope.CaptureCreatedAt) || !target.RequestStartedAt.IsZero() && target.RequestStartedAt.Before(targetRow.CreatedAt.Time) {
			return p, ErrConflict
		}
		database, e := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(target.TargetDatabaseID)})
		if e != nil {
			return p, checkpointPgCompensationDependencyError(mapErr(e))
		}
		if target.State == "retired" {
			if database.State != "deleted" || !database.DeletedAt.Valid || saved != nil && saved.State == "retired" && saved.Plan.Target != nil && saved.Plan.Target.State != "retired" && !database.DeletedAt.Time.Equal(saved.Observation.RetiredAt) {
				return p, ErrConflict
			}
			database.State, database.DeletedAt = "provisioning", pgtype.Timestamptz{}
		}
		if validateCloneDatabaseReservation(op, source, resource, point, database) != nil || database.State != "provisioning" || database.ObservedGeneration != 0 || database.DataResourceID.Valid || database.LeaseToken.Valid || database.LeaseUntil.Valid || database.ProviderResourceID.Valid != (target.ProviderResourceID != "") || database.ProviderResourceID.String != target.ProviderResourceID {
			return p, ErrConflict
		}
		if saved != nil && saved.State == "retired" {
			if saved.Plan.Target == nil || target.State != "retired" || targetRow.CheckpointRetirementObservationSha256.String != saved.ObservationSHA256 || !target.RetiredAt.Equal(saved.Observation.RetiredAt) {
				return p, ErrConflict
			}
			target.State, target.RetiredAt = saved.Plan.Target.State, saved.Plan.Target.RetiredAt
		} else if targetRow.CheckpointRetirementObservationSha256.Valid {
			return p, ErrConflict
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return p, mapErr(err)
	} else {
		// No row cannot hide an independently reserved target catalogue owner.
		if _, e := readCloneDatabaseReservationTx(ctx, tx, op, id); !errors.Is(e, pgx.ErrNoRows) {
			return p, ErrConflict
		}
	}
	if err = validateCheckpointPostgresCopyRetirementPlan(p); err != nil {
		return p, err
	}
	if saved != nil && !reflect.DeepEqual(p, saved.Plan) {
		return p, ErrConflict
	}
	return p, checkpointPgCopyRetirementReadyTx(ctx, tx, op, id)
}

func canonicalCheckpointCopyReader(r *ProjectEnvironmentClonePostgresCopyReader) {
	r.RequestStartedAt, r.EndpointCreatedAt, r.ObservedAt = r.RequestStartedAt.UTC(), r.EndpointCreatedAt.UTC(), r.ObservedAt.UTC()
	r.CleanupRequestedAt, r.CleanupDispatchedAt, r.RetiredAt = r.CleanupRequestedAt.UTC(), r.CleanupDispatchedAt.UTC(), r.RetiredAt.UTC()
}
func canonicalCheckpointCopyTarget(t *ProjectEnvironmentClonePostgresCopyTarget) {
	t.RequestStartedAt, t.ProviderCreatedAt, t.ObservedAt = t.RequestStartedAt.UTC(), t.ProviderCreatedAt.UTC(), t.ObservedAt.UTC()
	t.PreparedAt, t.RetiredAt, t.DeletionStartedAt, t.DeletionObservedAt = t.PreparedAt.UTC(), t.RetiredAt.UTC(), t.DeletionStartedAt.UTC(), t.DeletionObservedAt.UTC()
}
func checkpointPgCopyRetirementReadyTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) error {
	blocked, err := sqlc.New().CloneCheckpointPgCopyRetirementHasBorrowers(ctx, tx, sqlc.CloneCheckpointPgCopyRetirementHasBorrowersParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return mapErr(err)
	}
	if !blocked.Valid || blocked.Bool {
		return ErrConflict
	}
	return nil
}
func readCheckpointPgCopyRetirementTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error) {
	row, err := sqlc.New().ReadCloneCheckpointPgCopyRetirement(ctx, tx, sqlc.ReadCloneCheckpointPgCopyRetirementParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresCopyRetirement{}, mapErr(err)
	}
	in, err := checkpointPgCopyRetirementFromSQL(row)
	if err != nil {
		return in, err
	}
	_, err = checkpointPgCopyRetirementPlanTx(ctx, tx, op, id, &in)
	return in, checkpointPgCompensationDependencyError(err)
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCopyRetirement
	err := s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgCopyRetirementTx(ctx, tx, op, id)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointPgCopyRetirementPlanTx(ctx, tx, op, id, nil)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		row, err := sqlc.New().InsertCloneCheckpointPgCopyRetirement(ctx, tx, sqlc.InsertCloneCheckpointPgCopyRetirementParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Plan: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgCopyRetirementFromSQL(row)
		return err
	})
	return out, err
}
func (s *PgStore) ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCopyRetirement
	err := s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgCopyRetirementTx(ctx, tx, op, id)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCopyRetirement
	dispatch := false
	err := s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgCopyRetirementTx(ctx, tx, op, id)
		if err != nil || out.State != "planned" {
			return err
		}
		row, err := sqlc.New().ClaimCloneCheckpointPgCopyRetirement(ctx, tx, sqlc.ClaimCloneCheckpointPgCopyRetirementParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgCopyRetirementFromSQL(row)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) error {
	return s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointPgCopyRetirementTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresCopyRetirementObservation(in, o); err != nil {
			return err
		}
		if in.State == "retired" {
			return nil
		}
		raw, err := json.Marshal(o)
		if err != nil {
			return err
		}
		if len(raw) > api.PostgresCheckpointCopyRetirementMaxBytes {
			return ErrQuotaExceeded
		}
		q, digest := sqlc.New(), cloneCheckpointSHA(o)
		n, err := q.FinishCloneCheckpointPgCopyRetirement(ctx, tx, sqlc.FinishCloneCheckpointPgCopyRetirementParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Observation: raw, ObservationSha256: digest, RetiredAt: pgtype.Timestamptz{Time: o.RetiredAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		if in.Plan.Reader != nil {
			n, err = q.RetireCloneCheckpointPgCopyReader(ctx, tx, sqlc.RetireCloneCheckpointPgCopyReaderParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), OwnerID: mustPgUUID(in.Plan.Reader.OwnerID), ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
		if in.Plan.Target != nil {
			if in.Plan.Target.State != "retired" {
				n, err = q.RetireCloneCheckpointPgCopyDatabase(ctx, tx, sqlc.RetireCloneCheckpointPgCopyDatabaseParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
				if err != nil {
					return mapErr(err)
				}
				if n != 1 {
					return ErrConflict
				}
			}
			n, err = q.RetireCloneCheckpointPgCopyTarget(ctx, tx, sqlc.RetireCloneCheckpointPgCopyTargetParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), OwnerID: mustPgUUID(in.Plan.Target.TargetDatabaseID), ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
		_, err = readCheckpointPgCopyRetirementTx(ctx, tx, op, id)
		return err
	})
}

// Absence of borrowers grants no native IO. Every recorded dependency requires
// its complete native proof and the exact digest attached to its catalogue row.
func checkpointPgCopyDependenciesRetiredTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) error {
	q := sqlc.New()
	_, rerr := q.ReadProjectEnvironmentClonePostgresCopyReader(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresCopyReaderParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	_, terr := q.ReadProjectEnvironmentClonePostgresCopyTarget(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresCopyTargetParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if errors.Is(rerr, pgx.ErrNoRows) && errors.Is(terr, pgx.ErrNoRows) {
		_, err := q.ReadCloneCheckpointPgCopyRetirement(ctx, tx, sqlc.ReadCloneCheckpointPgCopyRetirementParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			if _, e := readCloneDatabaseReservationTx(ctx, tx, op, id); !errors.Is(e, pgx.ErrNoRows) {
				return ErrConflict
			}
			return nil
		}
		if err != nil {
			return mapErr(err)
		}
		return ErrConflict
	}
	if rerr != nil && !errors.Is(rerr, pgx.ErrNoRows) {
		return mapErr(rerr)
	}
	if terr != nil && !errors.Is(terr, pgx.ErrNoRows) {
		return mapErr(terr)
	}
	in, err := readCheckpointPgCopyRetirementTx(ctx, tx, op, id)
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	if in.State != "retired" {
		return ErrConflict
	}
	return nil
}

package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPostgresTargetCompensationStore = (*PgStore)(nil)

func canonicalCheckpointPgCompensationPlan(p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan {
	s, r := &p.Snapshot, &p.Restore
	s.CapturePoint, s.SnapshotCreatedAt, s.ObservedAt = s.CapturePoint.UTC(), s.SnapshotCreatedAt.UTC(), s.ObservedAt.UTC()
	s.RequestStartedAt, s.CleanupObservedAt = s.RequestStartedAt.UTC(), s.CleanupObservedAt.UTC()
	r.TargetCreatedAt, r.RequestStartedAt, r.ObservedAt = r.TargetCreatedAt.UTC(), r.RequestStartedAt.UTC(), r.ObservedAt.UTC()
	r.RestoredAt, r.DeletionStartedAt, r.DeletedAt, r.AdoptedAt = r.RestoredAt.UTC(), r.DeletionStartedAt.UTC(), r.DeletedAt.UTC(), r.AdoptedAt.UTC()
	return p
}

// Source configuration and provider health are deliberately not consulted. The
// original frozen binding, capture receipt, snapshot and restore pin authority.
func checkpointPgTargetCompensationPlanTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan, error) {
	var p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan
	if len(op.Resources) == 0 || op.Resources[0].Kind != "data_checkpoint" {
		return p, ErrConflict
	}
	_, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, l, id, false)
	if err != nil {
		return p, checkpointPgCompensationDependencyError(err)
	}
	return checkpointPgTargetCompensationOriginalPlanTx(ctx, tx, op, id, snapshot)
}

// Called only under an authenticated compensation operation lock. Capture
// retirement has already authenticated the full frozen adoption catalogue.
func checkpointPgTargetCompensationOriginalPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string, snapshot ProjectEnvironmentClonePostgresSnapshot) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan, error) {
	var p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return p, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return p, err
	}
	release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
	if err != nil {
		return p, checkpointPgCompensationDependencyError(err)
	}
	if release.State != "released" || !validCloneObjectSHA256(release.ObservationSHA256) || !validCloneObjectSHA256(release.AdoptionSHA256) || validateCloneCheckpointReleasePlanContents(op, release, views) != nil {
		return p, ErrConflict
	}

	if len(op.Resources) != len(release.Plan.Sources)+4 {
		return p, ErrConflict
	}
	roots := []ProjectEnvironmentCloneResource{
		{Kind: "data_checkpoint_adoption", Name: op.SourceEnvironment, SourceID: release.Receipt.CheckpointID, SourceVersion: release.AdoptionSHA256, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"},
		{Kind: "data_checkpoint_release", Name: op.SourceEnvironment, SourceID: release.Receipt.CheckpointID, SourceVersion: release.ObservationSHA256, CapturePoint: op.Resources[0].CapturePoint, Status: "captured"},
	}
	if !reflect.DeepEqual(op.Resources[len(op.Resources)-2:], roots) {
		return p, ErrConflict
	}
	found := false
	for n, source := range release.Plan.Sources {
		if source.Kind == "managed_postgres" && source.ID == id {
			if release.Receipt.Sources[n].CaptureIdentity != snapshot.ProviderSnapshotID || !release.Receipt.CapturedAt.Equal(snapshot.CapturePoint) {
				return p, ErrConflict
			}
			found = true
		}
	}
	if !found {
		return p, ErrConflict
	}
	restore, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return p, err
	}
	p.Snapshot, p.Restore = snapshot, restore
	isolation, err := readCheckpointPostgresIsolationTx(ctx, tx, op, id)
	if err == nil {
		if isolation.Plan.CheckpointID != release.Receipt.CheckpointID {
			return p, ErrConflict
		}
		p.Isolation = &isolation
	} else if !errors.Is(err, ErrNotFound) {
		return p, err
	} else if restore.AdoptedDatabaseID != "" {
		return p, ErrConflict
	}
	return canonicalCheckpointPgCompensationPlan(p), nil
}

func checkpointPgCompensationDependencyError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	return err
}

func checkpointPgTargetCompensationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPgTargetCompensation) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresTargetCompensation{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if len(r.Plan) > api.PostgresCheckpointTargetCompensationMaxBytes || json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Restore.OperationID != pgUUIDString(r.OperationID) || in.Plan.Restore.SourceDatabaseID != pgUUIDString(r.SourceDatabaseID) || validateCheckpointPostgresTargetCompensationPlan(in.Plan) != nil || !checkpointObjectRetirementJournalShape(in.State, "compensated", in.RequestedAt, in.ObservationSHA256) {
		return in, ErrConflict
	}
	if in.State == "compensated" && (len(r.Observation) > api.PostgresCheckpointTargetCompensationMaxBytes || json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointPostgresTargetCompensationObservation(in, in.Observation) != nil || !r.CompensatedAt.Valid || !in.Observation.CompensatedAt.Equal(r.CompensatedAt.Time)) {
		return in, ErrConflict
	}
	return in, nil
}

func readCheckpointPgTargetCompensationTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error) {
	row, err := sqlc.New().ReadCloneCheckpointPgTargetCompensation(ctx, tx, sqlc.ReadCloneCheckpointPgTargetCompensationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresTargetCompensation{}, mapErr(err)
	}
	in, err := checkpointPgTargetCompensationFromSQL(row)
	if err != nil {
		return in, err
	}
	actual, err := checkpointPgTargetCompensationPlanTx(ctx, tx, l, op, id)
	if err != nil {
		return in, checkpointPgCompensationDependencyError(err)
	}
	return in, checkpointPgTargetCompensationMatchesTx(ctx, tx, op, in, actual)
}

func checkpointPgTargetCompensationMatchesTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, actual ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) error {
	if in.State == "compensated" {
		restore, err := sqlc.New().ReadProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresSnapshotRestoreParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(in.Plan.Restore.SourceDatabaseID)})
		if err != nil {
			return mapErr(err)
		}
		if actual.Restore.State != "deleted" || restore.CheckpointCompensationObservationSha256.String != in.ObservationSHA256 {
			return ErrConflict
		}
		// Completed replay authenticates original physical identities after local
		// retirement, including later snapshot cleanup. It grants no fresh native IO.
		actual.Restore.State = in.Plan.Restore.State
		actual.Restore.DeletionStartedAt = in.Plan.Restore.DeletionStartedAt
		actual.Restore.DeletedAt = in.Plan.Restore.DeletedAt
		actual.Snapshot.State = in.Plan.Snapshot.State
		actual.Snapshot.CleanupObservedAt = in.Plan.Snapshot.CleanupObservedAt
	}
	if !reflect.DeepEqual(actual, in.Plan) {
		return ErrConflict
	}
	return nil
}

// A native target cannot be deleted while another journal still borrows it or
// while its independently prepared credentials retain authority. Read and
// validate every recorded retirement proof, rather than accepting a state flag.
func checkpointPgTargetCompensationReadyTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) error {
	q := sqlc.New()
	blocked, err := q.CloneCheckpointPgTargetCompensationBlocked(ctx, tx, sqlc.CloneCheckpointPgTargetCompensationBlockedParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(p.Restore.SourceDatabaseID), OwnerID: mustPgUUID(p.Restore.TargetOwnerID)})
	if err != nil {
		return mapErr(err)
	}
	if !blocked.Valid || blocked.Bool {
		return ErrConflict
	}
	if err = checkpointPgCopyDependenciesRetiredTx(ctx, tx, op, p.Restore.SourceDatabaseID); err != nil {
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

	credentials, err := q.ReadCloneCheckpointPgTargetCredentials(ctx, tx, sqlc.ReadCloneCheckpointPgTargetCredentialsParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(p.Restore.SourceDatabaseID)})
	if err != nil {
		return mapErr(err)
	}
	remaining := make(map[string]bool, len(credentials))
	for _, c := range credentials {
		remaining[pgUUIDString(c.SourceBindingID)] = true
	}
	for _, view := range views {
		for _, binding := range view.Postgres {
			if binding.DatabaseID != p.Restore.SourceDatabaseID {
				continue
			}
			c, err := readCheckpointPostgresCredentialTx(ctx, tx, op, binding.ID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			delete(remaining, binding.ID)
			cp := c.Plan
			if p.Isolation == nil || !reflect.DeepEqual(cp.Runtime.Verification.Isolation, p.Isolation.Plan) || cp.AppID != view.AppID || cp.Scope != op.TargetEnvironment || cp.EnvironmentKey != binding.EnvironmentKey || cp.Access != binding.Access {
				return ErrConflict
			}
			retirement, err := readCheckpointCredentialRetirementTx(ctx, tx, op, c)
			if err != nil {
				return checkpointPgCompensationDependencyError(err)
			}
			if retirement.State != "retired" || validateCheckpointPostgresCredentialRetirementObservation(retirement, retirement.Observation) != nil {
				return ErrConflict
			}
			published, err := q.CloneCheckpointPostgresCredentialPublished(ctx, tx, mustPgUUID(cp.TargetBindingID))
			if err != nil {
				return mapErr(err)
			}
			if !published.Valid || published.Bool {
				return ErrConflict
			}
		}
	}
	if len(remaining) != 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) checkpointPgTargetCompensationTx(ctx context.Context, l ProjectEnvironmentCloneLease, id string, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation) error) error {
	if !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	// Share the configuration-clock-before-project/app lock order used by source
	// compensation: catalogue retirement also runs configuration guard triggers.
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		if len(op.Resources) == 0 || op.Resources[0].Kind != "data_checkpoint" {
			return ErrConflict
		}
		return work(ctx, tx, op)
	})
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresTargetCompensation
	err := s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgTargetCompensationTx(ctx, tx, l, op, id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointPgTargetCompensationPlanTx(ctx, tx, l, op, id)
		if err != nil {
			return err
		}
		if validateCheckpointPostgresTargetCompensationPlan(p) != nil {
			return ErrConflict
		}
		if err = checkpointPgTargetCompensationReadyTx(ctx, tx, op, p); err != nil {
			return err
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if len(raw) > api.PostgresCheckpointTargetCompensationMaxBytes {
			return ErrConflict
		}
		row, err := sqlc.New().InsertCloneCheckpointPgTargetCompensation(ctx, tx, sqlc.InsertCloneCheckpointPgTargetCompensationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Plan: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgTargetCompensationFromSQL(row)
		return err
	})
	return out, err
}

func (s *PgStore) ProjectEnvironmentCloneCheckpointPostgresTargetCompensationForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresTargetCompensation
	err := s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgTargetCompensationTx(ctx, tx, l, op, id)
		return err
	})
	return out, err
}

func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresTargetCompensation
	dispatch := false
	err := s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgTargetCompensationTx(ctx, tx, l, op, id)
		if err != nil || out.State == "compensated" {
			return err
		}
		if err = checkpointPgTargetCompensationReadyTx(ctx, tx, op, out.Plan); err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		row, err := sqlc.New().ClaimCloneCheckpointPgTargetCompensation(ctx, tx, sqlc.ClaimCloneCheckpointPgTargetCompensationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgTargetCompensationFromSQL(row)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}

func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) error {
	return s.checkpointPgTargetCompensationTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointPgTargetCompensationTx(ctx, tx, l, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresTargetCompensationObservation(in, o); err != nil {
			return err
		}
		if in.State == "compensated" {
			return nil
		}
		if err = checkpointPgTargetCompensationReadyTx(ctx, tx, op, in.Plan); err != nil {
			return err
		}
		raw, err := json.Marshal(o)
		if err != nil {
			return err
		}
		if len(raw) > api.PostgresCheckpointTargetCompensationMaxBytes {
			return ErrConflict
		}
		q := sqlc.New()
		digest := cloneCheckpointSHA(o)
		n, err := q.FinishCloneCheckpointPgTargetCompensation(ctx, tx, sqlc.FinishCloneCheckpointPgTargetCompensationParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), Observation: raw, ObservationSha256: digest, CompensatedAt: pgtype.Timestamptz{Time: o.CompensatedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		if in.Plan.Restore.AdoptedDatabaseID != "" && in.Plan.Restore.State != "deleted" {
			n, err = q.RetireCloneCheckpointPgTargetDatabase(ctx, tx, sqlc.RetireCloneCheckpointPgTargetDatabaseParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ExpectedRevision: op.Revision, WorkerToken: l.Token})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
		n, err = q.FinishCloneCheckpointPgTargetRestore(ctx, tx, sqlc.FinishCloneCheckpointPgTargetRestoreParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(id), ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		// Re-read in the same transaction, proving catalogue and journal agree. Any
		// failed ownership check rolls back the proof, quota release and retirement.
		_, err = readCheckpointPgTargetCompensationTx(ctx, tx, l, op, id)
		return err
	})
}

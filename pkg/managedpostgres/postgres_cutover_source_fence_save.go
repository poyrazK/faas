package managedpostgres

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateSourceFenceProgress(old, next CutoverSourceFence) error {
	allowed := map[string][]string{"pending": {"pending", "closing"}, "closing": {"closing", "closed"}, "closed": {"closed"}, "abandoning": {"abandoning", "released"}}
	if !slices.Contains(allowed[old.State], next.State) {
		return ErrConflict
	}
	if old.OwnerToken != next.OwnerToken || old.Maintenance.OwnerToken != next.Maintenance.OwnerToken ||
		old.CatalogFingerprint != "" && old.CatalogFingerprint != next.CatalogFingerprint ||
		!old.CatalogInvalidatedAt.IsZero() && !old.CatalogInvalidatedAt.Equal(next.CatalogInvalidatedAt) ||
		next.Maintenance.SourceResourceID != old.Source.DataResourceID || old.Maintenance.OwnerOID != 0 && old.Maintenance.OwnerOID != next.Maintenance.OwnerOID ||
		old.Maintenance.DatabaseOID != 0 && old.Maintenance.DatabaseOID != next.Maintenance.DatabaseOID ||
		len(old.DatabaseNames) > 0 && !slices.Equal(old.DatabaseNames, next.DatabaseNames) ||
		!old.CloseRequestedAt.IsZero() && !old.CloseRequestedAt.Equal(next.CloseRequestedAt) ||
		!validErrorCode(next.LastErrorCode) && next.LastErrorCode != "" {
		return ErrConflict
	}
	a, b := sourceMaintenanceStageIndex(old.MaintenanceStage), sourceMaintenanceStageIndex(next.MaintenanceStage)
	if a < 0 || b < a || b > a+1 {
		return ErrConflict
	}
	if len(next.DatabaseNames) > 0 && next.request().Validate() != nil {
		return ErrConflict
	}
	if next.Observation != nil {
		if next.Observation.WriterIsolationCode != "" && !validErrorCode(next.Observation.WriterIsolationCode) {
			return ErrConflict
		}
		if proof := next.Observation.WriterIsolation; proof != nil {
			retained := old.Observation != nil && old.Observation.WriterIsolation != nil && *old.Observation.WriterIsolation == *proof
			if !retained && CheckpointWriterIsolationEvidence(*proof).Validate(next.writerIsolationRequest(), time.Now().UTC()) != nil {
				return ErrConflict
			}
		}
		if validateSourceFenceObservation(next) != nil || next.ObservedAt.IsZero() || next.ObservedAt.After(time.Now()) ||
			old.Observation != nil && !sameSourceFenceClosurePins(old.Observation, next.Observation) {
			return ErrConflict
		}
		if next.CatalogFingerprint != "" && old.CatalogFingerprint == "" && !sourceWriterCatalogComplete(next) {
			return ErrConflict
		}
	} else if old.Observation != nil {
		return ErrConflict
	}
	if next.State == "closing" && (next.CloseRequestedAt.IsZero() || next.MaintenanceStage != "ready" || len(next.DatabaseNames) == 0) {
		return ErrConflict
	}
	if next.State == "released" {
		if old.State != "abandoning" || next.RemoteTerminalState == "not_dispatched" && !old.CloseRequestedAt.IsZero() ||
			next.RemoteTerminalState != "not_dispatched" && (next.RemoteTerminalState != "released" && next.RemoteTerminalState != "abandoned" ||
				next.RemoteReleasedAt.IsZero() || next.RemoteReleasedAt.After(time.Now()) || old.CloseRequestedAt.IsZero()) {
			return ErrConflict
		}
	}
	return nil
}

func (s *PostgresStore) SaveCutoverSourceFence(ctx context.Context, expected, next CutoverSourceFence, keepLease bool, now, retry time.Time) (CutoverSourceFence, error) {
	if now.IsZero() || retry.Before(now) || expected.LeaseToken == "" || expected.CutoverID != next.CutoverID || expected.AccountID != next.AccountID {
		return CutoverSourceFence{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, accountStatus, err := lockSourceFenceCutover(ctx, tx, expected.AccountID, expected.CutoverID)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	q := sqlc.New()
	if _, err = q.LockManagedPostgresCutoverSourceFence(ctx, tx, c.ID); err != nil {
		return CutoverSourceFence{}, mapCutoverSourceFenceError(err)
	}
	old, err := readSourceFence(ctx, tx, c.AccountID, c.ID)
	if err != nil {
		return old, err
	}
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return old, err
	}
	if old.State == "released" || old.State != expected.State || old.LeaseToken != expected.LeaseToken || !old.LeaseUntil.After(clock.Time) ||
		old.MaintenanceStage != expected.MaintenanceStage || validateSourceFenceProgress(old, next) != nil {
		return old, ErrConflict
	}
	if old.State != "abandoning" && next.LastErrorCode == "" {
		if accountStatus == "deleted_pending" {
			return old, ErrConflict
		}
		if _, _, _, err := sourceFenceForwardInputs(ctx, tx, c, &old, now); err != nil {
			return old, err
		}
	}
	if next.MaintenanceStage != old.MaintenanceStage {
		if err := saveSourceMaintenance(ctx, tx, q, old, next, now); err != nil {
			return old, err
		}
	}
	var closure []byte
	if next.Observation != nil {
		closure, err = json.Marshal(next.Observation)
		if err != nil {
			return old, ErrInvalid
		}
	}
	n, err := q.SaveManagedPostgresCutoverSourceFence(ctx, tx, sqlc.SaveManagedPostgresCutoverSourceFenceParams{
		ID: c.ID, Token: expected.LeaseToken, State: next.State, Names: append([]string{}, next.DatabaseNames...),
		CatalogFingerprint:   next.CatalogFingerprint,
		CatalogInvalidatedAt: optionalSourceFenceTime(next.CatalogInvalidatedAt),
		CloseRequestedAt:     optionalSourceFenceTime(next.CloseRequestedAt), Closure: closure, ObservedAt: optionalSourceFenceTime(next.ObservedAt),
		ErrorCode: next.LastErrorCode, TerminalState: pgtype.Text{String: next.RemoteTerminalState, Valid: next.RemoteTerminalState != ""},
		RemoteReleasedAt: optionalSourceFenceTime(next.RemoteReleasedAt), KeepLease: keepLease, RetryAt: healthTimestamp(retry)})
	if err != nil {
		return old, mapCutoverSourceFenceError(err)
	}
	if n != 1 {
		return old, ErrConflict
	}
	out, err := readSourceFence(ctx, tx, c.AccountID, c.ID)
	if err != nil {
		return old, err
	}
	return out, mapCutoverSourceFenceError(tx.Commit(ctx))
}

func optionalSourceFenceTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func saveSourceMaintenance(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, old, next CutoverSourceFence, now time.Time) error {
	i := sourceMaintenanceStageIndex(next.MaintenanceStage)
	stamp := func(after int) pgtype.Timestamptz {
		if i >= after {
			return healthTimestamp(now)
		}
		return pgtype.Timestamptz{}
	}
	n, err := q.SaveManagedPostgresCutoverSourceMaintenance(ctx, db, sqlc.SaveManagedPostgresCutoverSourceMaintenanceParams{
		ID: old.Maintenance.OwnerToken, ExpectedState: old.MaintenanceStage, State: next.MaintenanceStage,
		OwnerOid:        pgtype.Int8{Int64: int64(next.Maintenance.OwnerOID), Valid: next.Maintenance.OwnerOID != 0},
		DatabaseOid:     pgtype.Int8{Int64: int64(next.Maintenance.DatabaseOID), Valid: next.Maintenance.DatabaseOID != 0},
		RoleRequestedAt: stamp(1), DatabaseRequestedAt: stamp(3), ActivationRequestedAt: stamp(5), ReadyAt: stamp(6)})
	if err != nil {
		return mapCutoverSourceFenceError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

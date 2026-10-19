package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ OperatorRestartStore = (*PgStore)(nil)

func operatorRestartUUID(id string) pgtype.UUID {
	var value pgtype.UUID
	_ = value.Scan(id)
	return value
}

func operatorIntentAttempt(row sqlc.OperatorIntent) OperatorIntent {
	i := OperatorIntent{ID: pgUUIDString(row.ID), Kind: OperatorIntentKind(row.Kind), TargetID: row.TargetID,
		ActorID: pgUUIDString(row.ActorID), Reason: row.Reason.String, Metadata: row.Metadata,
		Status: OperatorIntentStatus(row.Status), RequestedAt: row.RequestedAt.Time, Error: row.Error.String,
		SnapIDsMarkedStale: row.SnapIdsMarkedStale, CompletionAuditOutboxID: row.CompletionAuditOutboxID.Int64}
	i.ColdBootGeneration = row.ColdBootGeneration.Int64
	if row.ClaimToken.Valid {
		i.ClaimToken = pgUUIDString(row.ClaimToken)
	}
	if row.AccountID.Valid {
		text := pgUUIDString(row.AccountID)
		i.AccountID = &text
	}
	if row.TraceID.Valid {
		text := row.TraceID.String
		i.TraceID = &text
	}
	if row.StartedAt.Valid {
		at := row.StartedAt.Time
		i.StartedAt = &at
	}
	if row.FinishedAt.Valid {
		at := row.FinishedAt.Time
		i.FinishedAt = &at
	}
	return i
}

func operatorRestartExecution(row sqlc.OperatorRestartExecution) (OperatorRestartExecution, error) {
	e := OperatorRestartExecution{IntentID: pgUUIDString(row.IntentID), Phase: row.Phase, UpdatedAt: row.UpdatedAt.Time}
	if err := json.Unmarshal(row.Target, &e.Target); err != nil {
		return e, fmt.Errorf("decode operator restart target: %w", err)
	}
	if row.DestroyToken.Valid {
		e.DestroyToken = pgUUIDString(row.DestroyToken)
	}
	if row.DestroyedAt.Valid {
		at := row.DestroyedAt.Time
		e.DestroyedAt = &at
	}
	return e, nil
}

func (s *PgStore) ClaimPendingOperatorIntent(ctx context.Context) (OperatorIntent, error) {
	row, err := sqlc.New().ClaimOperatorIntentAttempt(ctx, s.pool, operatorRestartUUID(uuid.NewString()))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorIntent{}, ErrOperatorIntentNotFound
	}
	if err != nil {
		return OperatorIntent{}, fmt.Errorf("claim operator intent: %w", err)
	}
	return operatorIntentAttempt(row), nil
}

func (s *PgStore) GetOperatorIntent(ctx context.Context, id string) (OperatorIntent, error) {
	row, err := sqlc.New().GetOperatorIntentAttempt(ctx, s.pool, operatorRestartUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorIntent{}, ErrOperatorIntentNotFound
	}
	if err != nil {
		return OperatorIntent{}, fmt.Errorf("get operator intent: %w", err)
	}
	i := operatorIntentAttempt(row)
	if i.Kind == OperatorIntentKindForceRestart {
		e, readErr := s.GetOperatorRestart(ctx, id)
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return i, readErr
		}
		if readErr == nil {
			i.RestartExecution = &e
		}
	}
	if i.Kind == OperatorIntentKindForcePark {
		e, readErr := s.GetOperatorPark(ctx, id)
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return i, readErr
		}
		if readErr == nil {
			i.ParkExecution = &e
		}
	}
	return i, nil
}

func (s *PgStore) ReclaimStuckRunningOperatorIntents(ctx context.Context, threshold time.Time) (int, error) {
	n, err := sqlc.New().ReclaimOperatorIntentAttempts(ctx, s.pool, pgtype.Timestamptz{Time: threshold, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("reclaim operator intents: %w", err)
	}
	return int(n), nil
}

func (s *PgStore) GetOperatorRestart(ctx context.Context, id string) (OperatorRestartExecution, error) {
	row, err := sqlc.New().GetOperatorRestart(ctx, s.pool, operatorRestartUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorRestartExecution{}, ErrNotFound
	}
	if err != nil {
		return OperatorRestartExecution{}, fmt.Errorf("get operator restart: %w", err)
	}
	return operatorRestartExecution(row)
}

func lockOperatorRestartClaim(ctx context.Context, tx pgx.Tx, id, token string) (OperatorIntent, error) {
	row, err := sqlc.New().LockOperatorIntentAttempt(ctx, tx, operatorRestartUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorIntent{}, ErrOperatorIntentNotFound
	}
	if err != nil {
		return OperatorIntent{}, fmt.Errorf("lock restart claim: %w", err)
	}
	i := operatorIntentAttempt(row)
	if i.Kind != OperatorIntentKindForceRestart || i.ClaimToken != token {
		return i, ErrOperatorRestartClaimLost
	}
	return i, nil
}

func lockRestartInstance(ctx context.Context, tx pgx.Tx, target OperatorRestartTarget, requireOriginalLineage bool) (Instance, error) {
	r, err := sqlc.New().LockOperatorRestartInstance(ctx, tx, operatorRestartUUID(target.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrOperatorRestartTargetChanged
	}
	if err != nil {
		return Instance{}, fmt.Errorf("lock restart instance: %w", err)
	}
	i := Instance{ID: r.ID, AppID: r.AppID, DeploymentID: r.DeploymentID, NodeID: r.NodeID, WakeID: r.WakeID, State: r.State, LeaseToken: r.LeaseToken}
	if r.MigratedAt.Valid {
		at := r.MigratedAt.Time
		i.MigratedAt = &at
	}
	if !requireOriginalLineage {
		target.MigrationLeaseToken, target.MigratedAt = i.LeaseToken, i.MigratedAt
	}
	if !target.MatchesInstance(i) {
		return i, ErrOperatorRestartTargetChanged
	}
	return i, nil
}

func (s *PgStore) PrepareOperatorRestart(ctx context.Context, id, token string, target OperatorRestartTarget) (OperatorRestartExecution, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorRestartExecution{}, err
	}
	if err := validateRestartTarget(target); err != nil {
		return OperatorRestartExecution{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorRestartExecution{}, fmt.Errorf("prepare restart begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := lockOperatorRestartClaim(ctx, tx, id, token)
	if err != nil {
		return OperatorRestartExecution{}, err
	}
	if i.Status != OperatorIntentRunning {
		return OperatorRestartExecution{}, ErrOperatorRestartClaimLost
	}
	q := sqlc.New()
	existing, err := q.GetOperatorRestart(ctx, tx, operatorRestartUUID(id))
	if err == nil {
		return operatorRestartExecution(existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorRestartExecution{}, fmt.Errorf("prepare restart read: %w", err)
	}
	instance, err := lockRestartInstance(ctx, tx, target, false)
	if err != nil {
		return OperatorRestartExecution{}, err
	}
	if !sameRestartID(i.TargetID, target.InstanceID) || instance.State != string(StateRunning) {
		return OperatorRestartExecution{}, ErrOperatorRestartTargetChanged
	}
	target.MigrationLeaseToken, target.MigratedAt = instance.LeaseToken, instance.MigratedAt
	snaps, err := q.InvalidateOperatorRestartSnapshots(ctx, tx, operatorRestartUUID(target.DeploymentID))
	if err != nil {
		return OperatorRestartExecution{}, fmt.Errorf("prepare restart snapshots: %w", err)
	}
	ordered := make([]struct{ ID, Tier string }, len(snaps))
	for j, snap := range snaps {
		ordered[j] = struct{ ID, Tier string }{snap.ID, snap.Tier}
	}
	target.SnapshotIDs = sortRestartSnapshots(ordered)
	data, err := json.Marshal(target)
	if err != nil {
		return OperatorRestartExecution{}, fmt.Errorf("encode restart target: %w", err)
	}
	row, err := q.InsertOperatorRestart(ctx, tx, sqlc.InsertOperatorRestartParams{IntentID: operatorRestartUUID(id), Target: data})
	if err != nil {
		return OperatorRestartExecution{}, fmt.Errorf("prepare restart insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorRestartExecution{}, fmt.Errorf("prepare restart commit: %w", err)
	}
	return operatorRestartExecution(row)
}

func (s *PgStore) StartOperatorRestartDestroy(ctx context.Context, id, token, nodeID string) (OperatorRestartExecution, bool, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorRestartExecution{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorRestartExecution{}, false, fmt.Errorf("start restart begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := lockOperatorRestartClaim(ctx, tx, id, token)
	if err != nil {
		return OperatorRestartExecution{}, false, err
	}
	if i.Status != OperatorIntentRunning {
		return OperatorRestartExecution{}, false, ErrOperatorRestartClaimLost
	}
	q := sqlc.New()
	row, err := q.LockOperatorRestart(ctx, tx, operatorRestartUUID(id))
	if err != nil {
		return OperatorRestartExecution{}, false, fmt.Errorf("start restart read: %w", err)
	}
	e, err := operatorRestartExecution(row)
	if err != nil {
		return e, false, err
	}
	if !sameRestartID(e.Target.NodeID, nodeID) {
		return e, false, ErrOperatorRestartTargetChanged
	}
	if e.Phase != RestartPrepared {
		return e, false, nil
	}
	instance, err := lockRestartInstance(ctx, tx, e.Target, true)
	if err != nil {
		return e, false, err
	}
	if instance.State != string(StateRunning) {
		return e, false, ErrOperatorRestartTargetChanged
	}
	row, err = q.StartOperatorRestartDestroy(ctx, tx, sqlc.StartOperatorRestartDestroyParams{IntentID: operatorRestartUUID(id), DestroyToken: operatorRestartUUID(uuid.NewString())})
	if err != nil {
		return e, false, fmt.Errorf("start restart write: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return e, false, fmt.Errorf("start restart commit: %w", err)
	}
	e, err = operatorRestartExecution(row)
	return e, true, err
}

func (s *PgStore) AcknowledgeOperatorRestartDestroy(ctx context.Context, id, token string) error {
	if err := validRestartClaim(id, token); err != nil {
		return err
	}
	n, err := sqlc.New().AcknowledgeOperatorRestartDestroy(ctx, s.pool, sqlc.AcknowledgeOperatorRestartDestroyParams{IntentID: operatorRestartUUID(id), DestroyToken: operatorRestartUUID(token)})
	if err != nil {
		return fmt.Errorf("acknowledge restart destroy: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) CompleteOperatorRestart(ctx context.Context, input OperatorRestartCompletion) (OperatorRestartResult, error) {
	c, err := normalizeRestartCompletion(input)
	if err != nil {
		return OperatorRestartResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorRestartResult{}, fmt.Errorf("complete restart begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := lockOperatorRestartClaim(ctx, tx, c.IntentID, c.ClaimToken)
	if err != nil {
		return OperatorRestartResult{}, err
	}
	if i.Status != OperatorIntentRunning {
		if i.Status == c.Status && i.Error == c.Error && i.CompletionAuditOutboxID > 0 {
			return OperatorRestartResult{AuditOutboxID: i.CompletionAuditOutboxID, Replayed: true}, nil
		}
		return OperatorRestartResult{}, ErrConflict
	}
	q := sqlc.New()
	row, err := q.LockOperatorRestart(ctx, tx, operatorRestartUUID(i.ID))
	var e *OperatorRestartExecution
	if err == nil {
		value, decodeErr := operatorRestartExecution(row)
		if decodeErr != nil {
			return OperatorRestartResult{}, decodeErr
		}
		e = &value
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorRestartResult{}, fmt.Errorf("complete restart read: %w", err)
	}
	if c.Status == OperatorIntentFailed && e != nil && e.DestroyToken != "" {
		return OperatorRestartResult{}, fmt.Errorf("started restart requires a proven outcome or outcome_unknown: %w", ErrConflict)
	}
	if c.Status == OperatorIntentSucceeded {
		if e == nil || e.DestroyedAt == nil {
			return OperatorRestartResult{}, fmt.Errorf("restart lacks a durable teardown acknowledgement: %w", ErrConflict)
		}
		instance, readErr := lockRestartInstance(ctx, tx, e.Target, true)
		if readErr != nil {
			return OperatorRestartResult{}, readErr
		}
		if instance.State == string(StateRunning) {
			written, stopErr := q.StopOperatorRestartInstance(ctx, tx, sqlc.StopOperatorRestartInstanceParams{ID: operatorRestartUUID(instance.ID), NodeID: operatorRestartUUID(instance.NodeID), WakeID: operatorRestartUUID(instance.WakeID)})
			if stopErr != nil {
				return OperatorRestartResult{}, fmt.Errorf("complete restart state: %w", stopErr)
			}
			if written != 1 {
				return OperatorRestartResult{}, fmt.Errorf("restart stopped state update was not applied")
			}
		} else if instance.State != string(StateStopped) {
			return OperatorRestartResult{}, ErrOperatorRestartTargetChanged
		}
		if err := q.ReleaseOperatorRestartHostPorts(ctx, tx, sqlc.ReleaseOperatorRestartHostPortsParams{NodeID: operatorRestartUUID(instance.NodeID), InstanceID: operatorRestartUUID(instance.ID)}); err != nil {
			return OperatorRestartResult{}, fmt.Errorf("complete restart host ports: %w", err)
		}
		e.Phase = RestartCompleted
	} else if e != nil {
		if c.Status == OperatorIntentOutcomeUnknown {
			e.Phase = RestartOutcomeUnknown
			if e.DestroyedAt != nil {
				e.Phase = RestartDestroyed
			}
		} else {
			e.Phase = RestartFailed
		}
	}
	finished, err := q.OperatorRestartDatabaseTime(ctx, tx)
	if err != nil {
		return OperatorRestartResult{}, fmt.Errorf("complete restart clock: %w", err)
	}
	i.Status, i.Error, i.FinishedAt, i.SnapIDsMarkedStale = c.Status, c.Error, &finished.Time, []string{}
	if e != nil {
		i.SnapIDsMarkedStale = e.Target.SnapshotIDs
	}
	subject := pgtype.UUID{}
	if i.AccountID != nil {
		subject = operatorRestartUUID(*i.AccountID)
	}
	outboxID, err := q.EnqueueOperatorRestartOutcome(ctx, tx, sqlc.EnqueueOperatorRestartOutcomeParams{Subject: subject, Data: restartOutcomeAudit(i, e), DedupeKey: restartAuditKey(i)})
	if err != nil {
		return OperatorRestartResult{}, fmt.Errorf("complete restart audit: %w", err)
	}
	_, err = q.CompleteOperatorRestartIntent(ctx, tx, sqlc.CompleteOperatorRestartIntentParams{ID: operatorRestartUUID(i.ID), Status: string(c.Status), Error: c.Error, SnapIds: i.SnapIDsMarkedStale, OutboxID: outboxID, FinishedAt: finished})
	if err != nil {
		return OperatorRestartResult{}, fmt.Errorf("complete restart outcome: %w", err)
	}
	if e != nil {
		if err := q.SetOperatorRestartPhase(ctx, tx, sqlc.SetOperatorRestartPhaseParams{IntentID: operatorRestartUUID(i.ID), Phase: e.Phase}); err != nil {
			return OperatorRestartResult{}, fmt.Errorf("complete restart phase: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorRestartResult{}, fmt.Errorf("complete restart commit: %w", err)
	}
	return OperatorRestartResult{AuditOutboxID: outboxID}, nil
}

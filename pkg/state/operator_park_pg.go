package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ OperatorParkStore = (*PgStore)(nil)

func operatorParkExecution(row sqlc.OperatorParkExecution) (OperatorParkExecution, error) {
	e := OperatorParkExecution{IntentID: pgUUIDString(row.IntentID), Phase: row.Phase, UpdatedAt: row.UpdatedAt.Time, TerminalState: row.TerminalState.String}
	if err := json.Unmarshal(row.Target, &e.Target); err != nil {
		return e, fmt.Errorf("decode operator park target: %w", err)
	}
	if row.EffectToken.Valid {
		e.EffectToken = pgUUIDString(row.EffectToken)
	}
	if row.StoppedAt.Valid {
		at := row.StoppedAt.Time
		e.StoppedAt = &at
	}
	if len(row.Receipt) > 0 {
		var r OperatorParkReceipt
		if err := json.Unmarshal(row.Receipt, &r); err != nil {
			return e, err
		}
		if err := r.Validate(e.Target); err != nil {
			return e, err
		}
		e.Receipt = &r
	}
	return e, nil
}

func (s *PgStore) GetOperatorPark(ctx context.Context, id string) (OperatorParkExecution, error) {
	row, err := sqlc.New().GetOperatorPark(ctx, s.pool, operatorRestartUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorParkExecution{}, ErrNotFound
	}
	if err != nil {
		return OperatorParkExecution{}, err
	}
	return operatorParkExecution(row)
}

func lockOperatorParkClaim(ctx context.Context, tx pgx.Tx, id, token string) (OperatorIntent, error) {
	row, err := sqlc.New().LockOperatorIntentAttempt(ctx, tx, operatorRestartUUID(id))
	if err != nil {
		return OperatorIntent{}, mapErr(err)
	}
	i := operatorIntentAttempt(row)
	if i.Kind != OperatorIntentKindForcePark || i.ClaimToken != token {
		return i, ErrOperatorParkClaimLost
	}
	return i, nil
}

func lockParkInstance(ctx context.Context, tx pgx.Tx, target OperatorParkTarget, lineage bool) (Instance, bool, error) {
	r, err := sqlc.New().LockOperatorParkInstance(ctx, tx, operatorRestartUUID(target.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, false, ErrOperatorParkTargetChanged
	}
	if err != nil {
		return Instance{}, false, err
	}
	ins := Instance{ID: r.ID, AppID: r.AppID, DeploymentID: r.DeploymentID, NodeID: r.NodeID, WakeID: r.WakeID, State: r.State, Mode: r.Mode, StartedAt: r.StartedAt.Time, LastRequestAt: r.LastRequestAt.Time, RequestCount: r.RequestCount, LeaseToken: r.LeaseToken}
	if r.MigratedAt.Valid {
		at := r.MigratedAt.Time
		ins.MigratedAt = &at
	}
	if !lineage {
		target.MigrationLeaseToken, target.MigratedAt = ins.LeaseToken, ins.MigratedAt
	}
	if !target.MatchesInstance(ins) {
		return ins, r.ExclusiveCaptureBlocked, ErrOperatorParkTargetChanged
	}
	return ins, r.ExclusiveCaptureBlocked, nil
}

func lockParkParents(ctx context.Context, tx pgx.Tx, t OperatorParkTarget) error {
	q := sqlc.New()
	if _, err := q.LockSnapshotRecoveryApp(ctx, tx, operatorRestartUUID(t.AppID)); err != nil {
		return mapErr(err)
	}
	if _, err := q.LockSnapshotRecoveryDeployment(ctx, tx, sqlc.LockSnapshotRecoveryDeploymentParams{AppID: operatorRestartUUID(t.AppID), DeploymentID: operatorRestartUUID(t.DeploymentID)}); err != nil {
		return mapErr(err)
	}
	return nil
}

func writeParkState(ctx context.Context, tx pgx.Tx, id string, from, to State) error {
	if !CanTransition(from, to) {
		return ErrConflict
	}
	n, err := sqlc.New().SetOperatorParkInstanceState(ctx, tx, sqlc.SetOperatorParkInstanceStateParams{ID: operatorRestartUUID(id), State: string(to), ExpectedState: string(from)})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("operator park state update was not applied")
	}
	return nil
}

func (s *PgStore) PrepareOperatorPark(ctx context.Context, id, token string, target OperatorParkTarget) (OperatorParkExecution, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorParkExecution{}, err
	}
	if err := validateParkTarget(target); err != nil {
		return OperatorParkExecution{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorParkExecution{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := lockOperatorParkClaim(ctx, tx, id, token)
	if err != nil {
		return OperatorParkExecution{}, err
	}
	if i.Status != OperatorIntentRunning {
		return OperatorParkExecution{}, ErrOperatorParkClaimLost
	}
	q := sqlc.New()
	old, err := q.GetOperatorPark(ctx, tx, operatorRestartUUID(id))
	if err == nil {
		return operatorParkExecution(old)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorParkExecution{}, err
	}
	if err := lockParkParents(ctx, tx, target); err != nil {
		return OperatorParkExecution{}, err
	}
	ins, blocked, err := lockParkInstance(ctx, tx, target, false)
	if err != nil {
		return OperatorParkExecution{}, err
	}
	if !sameRestartID(i.TargetID, target.InstanceID) || ins.State != target.InitialState {
		return OperatorParkExecution{}, ErrOperatorParkTargetChanged
	}
	if ins.LastRequestAt.After(target.LastRequestAt) || ins.RequestCount > target.RequestCount {
		return OperatorParkExecution{}, ErrOperatorParkCancelled
	}
	target.MigrationLeaseToken, target.MigratedAt = ins.LeaseToken, ins.MigratedAt
	if target.Action == OperatorParkCapture {
		if blocked {
			return OperatorParkExecution{}, ErrConflict
		}
		busy, err := q.HasExclusiveSnapshotOwner(ctx, tx, ins.ID)
		if err != nil {
			return OperatorParkExecution{}, err
		}
		if busy {
			return OperatorParkExecution{}, ErrConflict
		}
		if err := writeParkState(ctx, tx, ins.ID, State(ins.State), StateSnapshotting); err != nil {
			return OperatorParkExecution{}, err
		}
		if err := writeParkBarrier(ctx, tx, ins.ID, true); err != nil {
			return OperatorParkExecution{}, err
		}
		if target.Measured {
			req := target.CaptureRequest()
			if err := req.validate(string(StateSnapshotting)); err != nil {
				return OperatorParkExecution{}, err
			}
			g, err := issueStandardSnapshotCaptureTx(ctx, tx, string(StateSnapshotting), req)
			if err != nil {
				return OperatorParkExecution{}, err
			}
			target.Grant = &g
		}
	}
	raw, err := json.Marshal(target)
	if err != nil {
		return OperatorParkExecution{}, err
	}
	row, err := q.InsertOperatorPark(ctx, tx, sqlc.InsertOperatorParkParams{IntentID: operatorRestartUUID(id), Target: raw})
	if err != nil {
		return OperatorParkExecution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorParkExecution{}, err
	}
	return operatorParkExecution(row)
}

func (s *PgStore) StartOperatorPark(ctx context.Context, id, token, nodeID string) (OperatorParkExecution, bool, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorParkExecution{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorParkExecution{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := lockOperatorParkClaim(ctx, tx, id, token)
	if err != nil {
		return OperatorParkExecution{}, false, err
	}
	if i.Status != OperatorIntentRunning {
		return OperatorParkExecution{}, false, ErrOperatorParkClaimLost
	}
	q := sqlc.New()
	row, err := q.LockOperatorPark(ctx, tx, operatorRestartUUID(id))
	if err != nil {
		return OperatorParkExecution{}, false, mapErr(err)
	}
	e, err := operatorParkExecution(row)
	if err != nil {
		return e, false, err
	}
	if !sameRestartID(e.Target.NodeID, nodeID) {
		return e, false, ErrOperatorParkTargetChanged
	}
	if e.Phase != ParkPrepared {
		return e, false, nil
	}
	ins, _, err := lockParkInstance(ctx, tx, e.Target, true)
	if err != nil {
		return e, false, err
	}
	if State(ins.State) != e.Target.PreparedState() {
		return e, false, ErrOperatorParkTargetChanged
	}
	row, err = q.StartOperatorPark(ctx, tx, sqlc.StartOperatorParkParams{IntentID: operatorRestartUUID(id), EffectToken: operatorRestartUUID(uuid.NewString())})
	if err != nil {
		return e, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return e, false, err
	}
	e, err = operatorParkExecution(row)
	return e, true, err
}

func (s *PgStore) AcknowledgeOperatorPark(ctx context.Context, id, token string, r OperatorParkReceipt) error {
	if err := validRestartClaim(id, token); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockOperatorPark(ctx, tx, operatorRestartUUID(id))
	if err != nil {
		return mapErr(err)
	}
	e, err := operatorParkExecution(row)
	if err != nil {
		return err
	}
	if e.EffectToken != token {
		return ErrConflict
	}
	if err := r.Validate(e.Target); err != nil {
		return err
	}
	if e.Receipt != nil {
		if sameParkReceipt(*e.Receipt, r) {
			return nil
		}
		return ErrConflict
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	n, err := q.AcknowledgeOperatorPark(ctx, tx, sqlc.AcknowledgeOperatorParkParams{IntentID: operatorRestartUUID(id), EffectToken: operatorRestartUUID(token), Receipt: raw})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) CompleteOperatorPark(ctx context.Context, input OperatorParkCompletion) (OperatorParkResult, error) {
	c, err := normalizeParkCompletion(input)
	if err != nil {
		return OperatorParkResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorParkResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := lockOperatorParkClaim(ctx, tx, c.IntentID, c.ClaimToken)
	if err != nil {
		return OperatorParkResult{}, err
	}
	if i.Status != OperatorIntentRunning {
		if i.Status == c.Status && i.Error == c.Error && i.CompletionAuditOutboxID > 0 {
			terminal := ""
			if i.Status == OperatorIntentSucceeded {
				row, err := sqlc.New().GetOperatorPark(ctx, tx, operatorRestartUUID(i.ID))
				if err != nil {
					return OperatorParkResult{}, err
				}
				terminal = row.TerminalState.String
			}
			return OperatorParkResult{AuditOutboxID: i.CompletionAuditOutboxID, Replayed: true, TerminalState: terminal}, nil
		}
		return OperatorParkResult{}, ErrConflict
	}
	q := sqlc.New()
	row, err := q.LockOperatorPark(ctx, tx, operatorRestartUUID(i.ID))
	var e *OperatorParkExecution
	if err == nil {
		value, err := operatorParkExecution(row)
		if err != nil {
			return OperatorParkResult{}, err
		}
		e = &value
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorParkResult{}, err
	}
	if (c.Status == OperatorIntentFailed || c.Status == OperatorIntentCancelled) && e != nil {
		return OperatorParkResult{}, fmt.Errorf("prepared park requires verified completion or outcome_unknown: %w", ErrConflict)
	}
	if c.Status == OperatorIntentSucceeded {
		if e == nil || e.Receipt == nil || e.StoppedAt == nil {
			return OperatorParkResult{}, ErrConflict
		}
		if err := lockParkParents(ctx, tx, e.Target); err != nil {
			return OperatorParkResult{}, err
		}
		ins, _, err := lockParkInstance(ctx, tx, e.Target, true)
		if err != nil {
			return OperatorParkResult{}, err
		}
		terminal := e.Receipt.Outcome
		if c.DiscardCapture {
			terminal = string(StateStopped)
		}
		if State(ins.State) != e.Target.PreparedState() && ins.State != terminal {
			return OperatorParkResult{}, ErrOperatorParkTargetChanged
		}
		if terminal == string(StateParked) && e.Target.Measured {
			record, err := q.GetApplicationStandardSnapshotCapture(ctx, tx, standardSnapshotQuery(*e.Target.Grant))
			if err != nil {
				return OperatorParkResult{}, err
			}
			capture, err := decodeStandardSnapshotRecord(record)
			if err != nil {
				return OperatorParkResult{}, err
			}
			if capture.Acknowledgment == nil || !capture.Acknowledgment.Equal(*e.Receipt.Acknowledgment) {
				return OperatorParkResult{}, ErrConflict
			}
		}
		if ins.State != terminal {
			if err := writeParkState(ctx, tx, ins.ID, State(ins.State), State(terminal)); err != nil {
				return OperatorParkResult{}, err
			}
		}
		if err := q.ReleaseOperatorRestartHostPorts(ctx, tx, sqlc.ReleaseOperatorRestartHostPortsParams{NodeID: operatorRestartUUID(ins.NodeID), InstanceID: operatorRestartUUID(ins.ID)}); err != nil {
			return OperatorParkResult{}, err
		}
		if e.Target.Action == OperatorParkCapture {
			if err := writeParkBarrier(ctx, tx, ins.ID, false); err != nil {
				return OperatorParkResult{}, err
			}
		}
		if terminal == string(StateParked) {
			payload := parkSnapshotNotification(*e)
			notificationID, err := q.EnqueueOperatorParkSnapshot(ctx, tx, string(payload))
			if err != nil {
				return OperatorParkResult{}, err
			}
			envelope, _ := json.Marshal(map[string]any{"_notification_outbox_id": notificationID, "_notification_payload": json.RawMessage(payload)})
			if err := q.NotifyOperatorParkSnapshot(ctx, tx, string(envelope)); err != nil {
				return OperatorParkResult{}, err
			}
		}
		e.Phase, e.TerminalState = ParkCompleted, terminal
	} else if e != nil {
		e.Phase = ParkOutcomeUnknown
		if e.Receipt != nil {
			e.Phase = ParkAcknowledged
		}
	}
	finished, err := q.OperatorRestartDatabaseTime(ctx, tx)
	if err != nil {
		return OperatorParkResult{}, err
	}
	i.Status, i.Error, i.FinishedAt = c.Status, c.Error, &finished.Time
	subject := pgtype.UUID{}
	if i.AccountID != nil {
		subject = operatorRestartUUID(*i.AccountID)
	}
	outboxID, err := q.EnqueueOperatorParkOutcome(ctx, tx, sqlc.EnqueueOperatorParkOutcomeParams{Subject: subject, Data: parkOutcomeAudit(i, e), DedupeKey: parkAuditKey(i)})
	if err != nil {
		return OperatorParkResult{}, err
	}
	_, err = q.CompleteOperatorRestartIntent(ctx, tx, sqlc.CompleteOperatorRestartIntentParams{ID: operatorRestartUUID(i.ID), Status: string(c.Status), Error: c.Error, SnapIds: []string{}, OutboxID: outboxID, FinishedAt: finished})
	if err != nil {
		return OperatorParkResult{}, err
	}
	if e != nil {
		written, phaseErr := q.SetOperatorParkPhase(ctx, tx, sqlc.SetOperatorParkPhaseParams{IntentID: operatorRestartUUID(i.ID), Phase: e.Phase, TerminalState: pgtype.Text{String: e.TerminalState, Valid: e.TerminalState != ""}})
		if phaseErr != nil {
			return OperatorParkResult{}, phaseErr
		}
		if written != 1 {
			return OperatorParkResult{}, fmt.Errorf("operator park phase update was not applied")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorParkResult{}, err
	}
	terminal := ""
	if e != nil {
		terminal = e.TerminalState
	}
	return OperatorParkResult{AuditOutboxID: outboxID, TerminalState: terminal}, nil
}

func (s *PgStore) OperatorParkHoldsInstance(ctx context.Context, id string) (bool, error) {
	return sqlc.New().OperatorParkHoldsInstance(ctx, s.pool, operatorRestartUUID(id))
}

func writeParkBarrier(ctx context.Context, tx pgx.Tx, id string, blocked bool) error {
	written, err := sqlc.New().SetOperatorParkBarrier(ctx, tx, sqlc.SetOperatorParkBarrierParams{ID: operatorRestartUUID(id), Blocked: blocked})
	if err != nil {
		return err
	}
	if written != 1 {
		return fmt.Errorf("operator park barrier update was not applied")
	}
	return nil
}

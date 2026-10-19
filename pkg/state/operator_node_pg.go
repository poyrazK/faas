package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ OperatorNodeStore = (*PgStore)(nil)

func (s *PgStore) ExecuteOperatorNode(ctx context.Context, id, token string) (OperatorNodeResult, error) {
	if err := validateSnapshotRecoveryIDs(id, token); err != nil {
		return OperatorNodeResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorNodeResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockOperatorIntentAttempt(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorNodeResult{}, ErrOperatorIntentNotFound
	}
	if err != nil {
		return OperatorNodeResult{}, fmt.Errorf("lock node intent: %w", err)
	}
	i := operatorIntentAttempt(row)
	if !IsOperatorNodeKind(i.Kind) || i.ClaimToken != token {
		return OperatorNodeResult{}, ErrOperatorNodeClaimLost
	}
	receipt, err := q.ReadOperatorNodeExecution(ctx, tx, mustPgUUID(id))
	if err == nil {
		if receipt.TargetID != i.TargetID || receipt.Kind != string(i.Kind) || pgUUIDString(receipt.ClaimToken) != token || receipt.Status != string(i.Status) {
			return OperatorNodeResult{}, ErrConflict
		}
		return OperatorNodeResult{NodeID: receipt.TargetID, Status: OperatorIntentStatus(receipt.Status), Error: receipt.Error,
			PreviousLifecycle: NodeLifecycle(receipt.PreviousLifecycle), Lifecycle: NodeLifecycle(receipt.ResultLifecycle),
			Changed: receipt.Changed, FinishedAt: receipt.FinishedAt.Time, AuditOutboxID: receipt.AuditOutboxID, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorNodeResult{}, fmt.Errorf("read node execution receipt: %w", err)
	}
	if i.Status != OperatorIntentRunning {
		return OperatorNodeResult{}, ErrOperatorNodeClaimLost
	}
	finished, err := q.OperatorRestartDatabaseTime(ctx, tx)
	if err != nil {
		return OperatorNodeResult{}, err
	}
	r := OperatorNodeResult{NodeID: i.TargetID, Status: OperatorIntentFailed, Error: "Compute node is unavailable.", FinishedAt: finished.Time}
	if err := applyOperatorNodeDB(ctx, tx, i, &r, finished); err != nil {
		return OperatorNodeResult{}, err
	}
	subject := pgtype.UUID{}
	if i.AccountID != nil {
		subject = mustPgUUID(*i.AccountID)
	}
	r.AuditOutboxID, err = q.EnqueueOperatorNodeOutcome(ctx, tx, sqlc.EnqueueOperatorNodeOutcomeParams{
		Kind: "operator.action." + string(i.Kind) + ".outcome", Subject: subject,
		Data: operatorNodeOutcomeAudit(i, r), DedupeKey: "operator.node.outcome:" + i.ID,
	})
	if err != nil {
		return OperatorNodeResult{}, fmt.Errorf("enqueue node outcome: %w", err)
	}
	if _, err := q.InsertOperatorNodeExecution(ctx, tx, sqlc.InsertOperatorNodeExecutionParams{
		ID: mustPgUUID(id), ClaimToken: mustPgUUID(token), Kind: string(i.Kind), TargetID: i.TargetID,
		PreviousLifecycle: string(r.PreviousLifecycle), ResultLifecycle: string(r.Lifecycle), Changed: r.Changed,
		Status: string(r.Status), Error: r.Error, FinishedAt: finished, OutboxID: r.AuditOutboxID,
	}); err != nil {
		return OperatorNodeResult{}, fmt.Errorf("save node execution receipt: %w", err)
	}
	if _, err := q.CompleteOperatorNode(ctx, tx, sqlc.CompleteOperatorNodeParams{
		ID: mustPgUUID(id), ClaimToken: mustPgUUID(token), Status: string(r.Status), Error: r.Error,
		FinishedAt: finished, OutboxID: r.AuditOutboxID,
	}); err != nil {
		return OperatorNodeResult{}, fmt.Errorf("complete node intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorNodeResult{}, fmt.Errorf("commit node intent: %w", err)
	}
	return r, nil
}

func applyOperatorNodeDB(ctx context.Context, tx pgx.Tx, i OperatorIntent, r *OperatorNodeResult, finished pgtype.Timestamptz) error {
	if validateSnapshotRecoveryIDs(i.TargetID) != nil {
		return nil //nolint:nilerr // Invalid stored target is a failed outcome; the caller commits its receipt and audit.
	}
	q := sqlc.New()
	if _, err := q.LockOperatorNodeCapacity(ctx, tx); err != nil {
		return fmt.Errorf("lock node capacity policy: %w", err)
	}
	node, err := q.LockOperatorNodeTarget(ctx, tx, mustPgUUID(i.TargetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil //nolint:nilerr // Missing target is a failed outcome; the caller commits its receipt and audit.
	}
	if err != nil {
		return fmt.Errorf("lock node target: %w", err)
	}
	r.PreviousLifecycle, r.Lifecycle = NodeLifecycle(node.Lifecycle), NodeLifecycle(node.Lifecycle)
	hasLive := false
	if i.Kind == OperatorIntentKindNodeRetire {
		hasLive, err = q.OperatorNodeHasLiveInstances(ctx, tx, mustPgUUID(i.TargetID))
		if err != nil {
			return fmt.Errorf("recheck node retirement: %w", err)
		}
	}
	next, err := operatorNodeTransition(i.Kind, ComputeNode{ID: node.ID, Name: node.Name, Lifecycle: r.PreviousLifecycle}, hasLive)
	if err != nil {
		r.Error = err.Error()
		return nil //nolint:nilerr // Policy refusal is a failed outcome, not a transaction failure.
	}
	r.Status, r.Error, r.Lifecycle, r.Changed = OperatorIntentSucceeded, "", next, next != r.PreviousLifecycle
	if !r.Changed {
		return nil
	}
	n, err := q.NodeSetLifecycle(ctx, tx, sqlc.NodeSetLifecycleParams{
		ID: mustPgUUID(i.TargetID), Lifecycle: sqlc.ComputeNodeLifecycle(r.PreviousLifecycle),
		Column3: sqlc.ComputeNodeLifecycle(next), DrainInitiatedAt: finished,
	})
	if err != nil {
		return fmt.Errorf("apply node lifecycle: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("node lifecycle write was suppressed: %w", ErrConflict)
	}
	return nil
}

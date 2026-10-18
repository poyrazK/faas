package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ NodeLifecycleCompletionStore = (*PgStore)(nil)

func (s *PgStore) CompleteNodeLifecycle(ctx context.Context, in NodeLifecycleCompletion) (NodeLifecycleCompletionResult, error) {
	if err := in.validate(); err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	for attempt := range 2 {
		receipt, err := q.ReadNodeLifecycleCompletion(ctx, tx, sqlc.ReadNodeLifecycleCompletionParams{NodeID: mustPgUUID(in.NodeID), Generation: in.Generation})
		if err == nil {
			if receipt.SourceLifecycle != string(in.Lifecycle) {
				return NodeLifecycleCompletionResult{}, ErrNodeLifecycleChanged
			}
			return nodeLifecycleCompletionReceipt(receipt), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return NodeLifecycleCompletionResult{}, fmt.Errorf("read node completion receipt: %w", err)
		}
		if attempt == 0 {
			if _, err := q.LockOperatorNodeCapacity(ctx, tx); err != nil {
				return NodeLifecycleCompletionResult{}, fmt.Errorf("lock node completion capacity: %w", err)
			}
		}
	}
	node, err := q.LockNodeLifecycleCompletionTarget(ctx, tx, mustPgUUID(in.NodeID))
	if err != nil {
		return NodeLifecycleCompletionResult{}, mapErr(err)
	}
	if node.LifecycleGeneration != in.Generation || node.Lifecycle != string(in.Lifecycle) {
		return NodeLifecycleCompletionResult{}, ErrNodeLifecycleChanged
	}
	eligible, err := q.NodeLifecycleCompletionEligible(ctx, tx, sqlc.NodeLifecycleCompletionEligibleParams{NodeID: mustPgUUID(in.NodeID), Lifecycle: string(in.Lifecycle)})
	if err != nil {
		return NodeLifecycleCompletionResult{}, fmt.Errorf("check node completion work: %w", err)
	}
	if !eligible.Valid || !eligible.Bool {
		return NodeLifecycleCompletionResult{}, ErrConflict
	}
	now, err := q.OperatorRestartDatabaseTime(ctx, tx)
	if err != nil {
		return NodeLifecycleCompletionResult{}, err
	}
	r := nodeCompletionResult(in, ComputeNode{Name: node.Name, DrainInitiatedAt: timestamptzToTimePtr(node.DrainInitiatedAt), RecoveryInitiatedAt: timestamptzToTimePtr(node.RecoveryInitiatedAt)}, now.Time)
	r.AuditOutboxID, err = q.EnqueueOperatorNodeOutcome(ctx, tx, sqlc.EnqueueOperatorNodeOutcomeParams{Kind: r.Kind(), Data: r.auditData(), DedupeKey: r.dedupeKey()})
	if err != nil {
		return NodeLifecycleCompletionResult{}, fmt.Errorf("enqueue node completion audit: %w", err)
	}
	if _, err := q.InsertNodeLifecycleCompletion(ctx, tx, sqlc.InsertNodeLifecycleCompletionParams{
		NodeID: mustPgUUID(in.NodeID), Generation: in.Generation, ResultGeneration: r.ResultGeneration,
		SourceLifecycle: string(in.Lifecycle), ResultLifecycle: string(r.ResultLifecycle), NodeName: r.NodeName,
		InitiatedAt: pgtype.Timestamptz{Time: r.InitiatedAt, Valid: true}, CompletedAt: now,
		DrainedCount: in.DrainedCount, MigratedCount: in.MigratedCount, RecreatedCount: in.RecreatedCount, OutboxID: r.AuditOutboxID,
	}); err != nil {
		return NodeLifecycleCompletionResult{}, fmt.Errorf("save node completion receipt: %w", err)
	}
	generation, err := q.FinishNodeLifecycleCompletion(ctx, tx, sqlc.FinishNodeLifecycleCompletionParams{
		NodeID: mustPgUUID(in.NodeID), Generation: in.Generation, SourceLifecycle: string(in.Lifecycle), ResultLifecycle: sqlc.ComputeNodeLifecycle(r.ResultLifecycle), CompletedAt: now,
	})
	if err != nil {
		return NodeLifecycleCompletionResult{}, fmt.Errorf("finish node lifecycle: %w", err)
	}
	if generation != r.ResultGeneration {
		return NodeLifecycleCompletionResult{}, ErrNodeLifecycleChanged
	}
	if err := tx.Commit(ctx); err != nil {
		return NodeLifecycleCompletionResult{}, fmt.Errorf("commit node lifecycle completion: %w", err)
	}
	return r, nil
}

func nodeLifecycleCompletionReceipt(row sqlc.NodeLifecycleCompletion) NodeLifecycleCompletionResult {
	return NodeLifecycleCompletionResult{NodeLifecycleCompletion: NodeLifecycleCompletion{
		NodeID: pgUUIDString(row.NodeID), Generation: row.SourceGeneration, Lifecycle: NodeLifecycle(row.SourceLifecycle),
		DrainedCount: row.DrainedCount, MigratedCount: row.MigratedCount, RecreatedCount: row.RecreatedCount},
		ResultGeneration: row.ResultGeneration, ResultLifecycle: NodeLifecycle(row.ResultLifecycle), NodeName: row.NodeName,
		InitiatedAt: row.InitiatedAt.Time, CompletedAt: row.CompletedAt.Time, AuditOutboxID: row.AuditOutboxID, Replayed: true}
}

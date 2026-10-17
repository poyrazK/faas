package internalmail

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func retentionResult(policy RetentionPolicy, dryRun bool) RetentionResult {
	return RetentionResult{DryRun: dryRun, Days: policy.Days, BatchSize: policy.BatchSize}
}

func (s *PGStore) previewRetention(ctx context.Context, policy RetentionPolicy) (RetentionResult, error) {
	result := retentionResult(policy, true)
	rows, err := s.queries.PreviewRetention(ctx, s.pool, q.PreviewRetentionParams{Namespace: s.namespace, RetentionDays: int32(policy.Days), BatchSize: int32(policy.BatchSize)})
	if err != nil {
		return result, err
	}
	result.EligibleMessages = len(rows)
	result.BatchFull = len(rows) == policy.BatchSize
	for _, size := range rows {
		result.EligibleBytes += size
	}
	return result, nil
}

func (s *PGStore) applyRetention(ctx context.Context, policy RetentionPolicy, prepare func(Message) ([]byte, error), enabled func() bool) (RetentionResult, error) {
	result := retentionResult(policy, false)
	if enabled == nil || !enabled() {
		return result, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.InternalMailRetentionRollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	result, err = s.prunePayloads(ctx, tx, policy, prepare, enabled)
	if err != nil {
		return retentionResult(policy, false), err
	}
	if !enabled() {
		return retentionResult(policy, false), ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return retentionResult(policy, false), err
	}
	return result, nil
}

// The caller owns the transaction so operator cleanup and its audit record
// share one commit. The automatic worker uses the same pruning safeguards.
func (s *PGStore) prunePayloads(ctx context.Context, tx pgx.Tx, policy RetentionPolicy, prepare func(Message) ([]byte, error), enabled func() bool) (RetentionResult, error) {
	result := retentionResult(policy, false)
	if err := s.shareRecovery(ctx, tx, ""); err != nil {
		if errors.Is(err, ErrRecoveryHeld) {
			return result, nil
		}
		return result, err
	}
	if err := s.shareEncryption(ctx, tx); err != nil {
		return result, err
	}
	rows, err := s.queries.RetentionCandidates(ctx, tx, q.RetentionCandidatesParams{Namespace: s.namespace, RetentionDays: int32(policy.Days), BatchSize: int32(policy.BatchSize)})
	if err != nil {
		return result, err
	}
	result.EligibleMessages, result.BatchFull = len(rows), len(rows) == policy.BatchSize
	for _, row := range rows {
		result.EligibleBytes += row.PayloadBytes
		locked, err := s.queries.LockRetentionRecipients(ctx, tx, q.LockRetentionRecipientsParams{Namespace: s.namespace, MessageID: row.ID})
		if err != nil {
			return retentionResult(policy, false), err
		}
		if int64(len(locked)) != row.RecipientCount {
			result.SkippedMessages++
			continue
		}
		m, err := s.queries.Message(ctx, tx, q.MessageParams{Namespace: s.namespace, ID: row.ID})
		if err != nil {
			return retentionResult(policy, false), err
		}
		fingerprint, err := prepare(messageRow(m))
		if err != nil {
			return retentionResult(policy, false), err
		}
		if !enabled() {
			return retentionResult(policy, false), ErrDisabled
		}
		if err := s.checkEncryptionWrite(ctx, tx, nil, fingerprint, true); err != nil {
			return retentionResult(policy, false), err
		}
		n, err := s.queries.PrunePayload(ctx, tx, q.PrunePayloadParams{Namespace: s.namespace, ID: row.ID, RetentionDays: int32(policy.Days), Fingerprint: fingerprint})
		if err != nil {
			return retentionResult(policy, false), err
		}
		result.PrunedMessages += int(n)
		if n == 1 {
			result.PrunedBytes += row.PayloadBytes
		} else {
			result.SkippedMessages++
		}
	}
	if !enabled() {
		return retentionResult(policy, false), ErrDisabled
	}
	return result, nil
}

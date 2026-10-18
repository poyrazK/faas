package outbound

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// ReserveAIBatch stores a pending batch and its reservation (ADR-1431).
// Admission counts it from this commit until SettleAIBatch.
func (b *PostgresBackend) ReserveAIBatch(ctx context.Context, batch AIBatch) error {
	id, err := uuid.Parse(batch.ID)
	if err != nil {
		return fmt.Errorf("%w: batch id must be a UUID", ErrInvalidIntegration)
	}
	integrationID, err := uuid.Parse(batch.IntegrationID)
	if err != nil {
		return fmt.Errorf("%w: batch integration id must be a UUID", ErrInvalidIntegration)
	}
	var bindingApp *uuid.UUID
	if parsed, err := uuid.Parse(batch.BindingAppID); err == nil {
		bindingApp = &parsed
	}
	_, err = b.pool.Exec(ctx, `
		INSERT INTO outbound_ai_batches
		    (id, integration_id, account_id, caller, binding_app_id, provider, request_path, request_query, model,
		     requests, reserved_tokens, reserved_cost_millicents)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, integrationID, batch.AccountID, batch.Caller, bindingApp, batch.Provider, batch.Path, batch.RawQuery,
		batch.Model, batch.Requests, batch.ReservedTokens, batch.ReservedCostMillicents)
	return err
}

// ConfirmAIBatch opens a pending batch with its provider batch ID.
func (b *PostgresBackend) ConfirmAIBatch(ctx context.Context, id, providerBatchID string) error {
	batchID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, `
		UPDATE outbound_ai_batches
		   SET state = 'open', provider_batch_id = $2, next_check_at = now() + make_interval(secs => $3::int)
		 WHERE id = $1 AND state = 'pending'`, batchID, providerBatchID, api.OutboundAIBatchPollIntervalSeconds)
	return err
}

// DropAIBatch deletes a pending batch the provider refused.
func (b *PostgresBackend) DropAIBatch(ctx context.Context, id string) error {
	batchID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, `DELETE FROM outbound_ai_batches WHERE id = $1 AND state = 'pending'`, batchID)
	return err
}

// ClaimAIBatches deletes pending batches older than their expiry, then
// returns up to limit open batches due for a check, each pushed back by the
// poll interval. A replica that claims a batch and stops leaves it for the
// next check.
func (b *PostgresBackend) ClaimAIBatches(ctx context.Context, limit int) ([]AIBatch, error) {
	if _, err := b.pool.Exec(ctx, `
		DELETE FROM outbound_ai_batches
		 WHERE state = 'pending' AND created_at < now() - make_interval(mins => $1::int)`,
		api.OutboundAIBatchPendingExpiryMinutes); err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, `
		UPDATE outbound_ai_batches batch
		   SET next_check_at = now() + make_interval(secs => $2::int), checks = batch.checks + 1
		 WHERE batch.id IN (SELECT due.id FROM outbound_ai_batches due
		                     WHERE due.state = 'open' AND due.next_check_at <= now()
		                     ORDER BY due.next_check_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING batch.id::text, batch.integration_id::text, batch.account_id::text, batch.caller,
		          COALESCE(batch.binding_app_id::text, ''), batch.provider, batch.provider_batch_id, batch.request_path,
		          batch.request_query, batch.model, batch.requests, batch.reserved_tokens, batch.reserved_cost_millicents,
		          batch.created_at`, limit, api.OutboundAIBatchPollIntervalSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []AIBatch
	for rows.Next() {
		var batch AIBatch
		if err := rows.Scan(&batch.ID, &batch.IntegrationID, &batch.AccountID, &batch.Caller, &batch.BindingAppID, &batch.Provider,
			&batch.ProviderBatchID, &batch.Path, &batch.RawQuery, &batch.Model, &batch.Requests, &batch.ReservedTokens,
			&batch.ReservedCostMillicents, &batch.CreatedAt); err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

// SettleAIBatch ends an open batch with what it settled, which releases its
// reservation.
func (b *PostgresBackend) SettleAIBatch(ctx context.Context, id string, outcome AIUsageOutcome, tokens, costMillicents int64) error {
	batchID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, `
		UPDATE outbound_ai_batches
		   SET state = 'settled', outcome = $2, settled_tokens = $3, settled_cost_millicents = $4, settled_at = now()
		 WHERE id = $1 AND state = 'open'`, batchID, string(outcome), max(tokens, 0), max(costMillicents, 0))
	return err
}

var _ AIBatchBackend = (*PostgresBackend)(nil)

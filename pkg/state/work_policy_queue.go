package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ClaimQueueTriggerInvocation is the production named queue poller's claim
// path. Environment ownership is checked before any lane, row or binding lock;
// stage work can never be adopted by this production consumer.
func (s *PgStore) ClaimQueueTriggerInvocation(ctx context.Context, id, triggerID, appID, queueName string, leaseSeconds int) (Invocation, error) {
	if queueName == "" || leaseSeconds <= 0 {
		return Invocation{}, ErrInvalidArgument
	}
	for _, value := range []string{id, triggerID, appID} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return Invocation{}, ErrInvalidArgument
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockInvocationEnvironmentClaimDB(ctx, tx, id); err != nil {
		return Invocation{}, err
	}
	queries := sqlc.New()
	lookup, err := queries.ReadProductionQueueTriggerInvocation(ctx, tx, sqlc.ReadProductionQueueTriggerInvocationParams{
		InvocationID: mustPgUUID(id), AppID: mustPgUUID(appID),
	})
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	if lookup.WorkPolicyName.Valid {
		if err := lockKeyedClaimTx(ctx, tx, id, appID, lookup.WorkPolicyName.String, lookup.WorkKeyDigest); err != nil {
			return Invocation{}, err
		}
		if lookup.WorkFairnessLimit.Valid {
			if err := lockFairnessClaimTx(ctx, tx, appID, lookup.WorkPolicyName.String, lookup.WorkFairnessDigest, int(lookup.WorkFairnessLimit.Int32)); err != nil {
				return Invocation{}, err
			}
		}
	}
	// Candidates are lock-free; skip a row claimed by another poller before
	// taking the binding lock that serializes the concurrency cap.
	if _, err := queries.LockProductionQueueTriggerInvocationRow(ctx, tx, sqlc.LockProductionQueueTriggerInvocationRowParams{
		InvocationID: mustPgUUID(id), AppID: mustPgUUID(appID),
	}); err != nil {
		return Invocation{}, mapErr(err)
	}
	cap, err := queries.LockProductionQueueBindingCap(ctx, tx, sqlc.LockProductionQueueBindingCapParams{
		AppID: mustPgUUID(appID), QueueName: queueName,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, fmt.Errorf("state: queue trigger binding cap: %w", err)
	}
	if err == nil {
		active, err := queries.CountProductionQueueBindingActive(ctx, tx, sqlc.CountProductionQueueBindingActiveParams{
			AppID: mustPgUUID(appID), QueueName: queueName,
		})
		if err != nil {
			return Invocation{}, fmt.Errorf("state: queue trigger active count: %w", err)
		}
		if active >= int64(cap) {
			return Invocation{}, ErrQuotaExceeded
		}
	}
	row, err := queries.ClaimProductionQueueTriggerInvocation(ctx, tx, sqlc.ClaimProductionQueueTriggerInvocationParams{
		InvocationID: mustPgUUID(id), AppID: mustPgUUID(appID), QueueName: queueName, TriggerID: mustPgUUID(triggerID),
		Lease: strconv.Itoa(leaseSeconds) + " seconds",
	})
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim commit: %w", err)
	}
	return invocationFromSQLC(row), nil
}

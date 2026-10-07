package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ClaimQueueTriggerInvocation is the production named queue poller's claim
// path. Environment ownership is checked before any lane, row or binding lock;
// stage work can never be adopted by this production consumer.
func (s *PgStore) ClaimQueueTriggerInvocation(ctx context.Context, id, triggerID, appID, queueName string, leaseSeconds int) (Invocation, error) {
	if queueName == "" || leaseSeconds <= 0 || int64(leaseSeconds) > 2147483647 {
		return Invocation{}, ErrInvalidArgument
	}
	var appUUID, triggerUUID pgtype.UUID
	for _, item := range []struct {
		value string
		out   *pgtype.UUID
	}{{id, nil}, {appID, &appUUID}, {triggerID, &triggerUUID}} {
		parsed, err := uuid.Parse(item.value)
		if err != nil || parsed == uuid.Nil {
			return Invocation{}, ErrInvalidArgument
		}
		if item.out != nil {
			*item.out = pgtype.UUID{Bytes: parsed, Valid: true}
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
	lookup, err := queries.ReadBoundOrProductionQueueTriggerInvocation(ctx, tx, sqlc.ReadBoundOrProductionQueueTriggerInvocationParams{
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
	if _, err := queries.LockBoundOrProductionQueueTriggerInvocation(ctx, tx, sqlc.LockBoundOrProductionQueueTriggerInvocationParams{
		InvocationID: mustPgUUID(id), AppID: mustPgUUID(appID),
	}); err != nil {
		return Invocation{}, mapErr(err)
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger row lock: %w", err)
	}
	maxConcurrency, bindingID, bindingScope, err := queueConsumerClaimCapTx(ctx, tx, appUUID, triggerUUID, queueName)
	if err != nil {
		return Invocation{}, err
	}
	if maxConcurrency > 0 {
		active, err := sqlc.New().QueueClaimActiveCount(ctx, tx, sqlc.QueueClaimActiveCountParams{
			AppID: appUUID, TriggerID: triggerUUID, BindingID: bindingID, BindingScope: bindingScope, QueueName: queueName,
		})
		if err != nil {
			return Invocation{}, fmt.Errorf("state: queue trigger active count: %w", err)
		}
		if active >= int64(maxConcurrency) {
			return Invocation{}, ErrQuotaExceeded
		}
	}
	row, err := sqlc.New().QueueClaimPendingInvocation(ctx, tx, sqlc.QueueClaimPendingInvocationParams{
		ID: pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true}, AppID: appUUID, TriggerID: triggerUUID,
		QueueName: queueName, BindingID: bindingID, BindingScope: bindingScope, LeaseSeconds: int32(leaseSeconds),
	})
	if err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim update: %w", mapErr(err))
	}
	claimed, err := invocationFromSQL(row)
	if err != nil {
		return Invocation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim commit: %w", err)
	}
	return claimed, nil
}

func queueConsumerClaimCapTx(ctx context.Context, tx pgx.Tx, appID, triggerID pgtype.UUID, queueName string) (int, pgtype.UUID, string, error) {
	q := sqlc.New()
	identity, err := q.QueueClaimConsumerIdentity(ctx, tx, sqlc.QueueClaimConsumerIdentityParams{ID: triggerID, AppID: appID})
	if err != nil {
		return 0, pgtype.UUID{}, "", mapErr(err)
	}
	var cap int32
	bindingID := identity.QueueBindingID
	if identity.QueueBindingID.Valid {
		cap, err = q.QueueClaimLockBinding(ctx, tx, sqlc.QueueClaimLockBindingParams{ID: identity.QueueBindingID, AppID: appID, BindingScope: identity.QueueBindingScope, QueueName: queueName})
		if err != nil {
			return 0, pgtype.UUID{}, "", mapErr(err)
		}
	} else {
		if identity.HasMarker {
			return 0, pgtype.UUID{}, "", ErrConflict
		}
		var legacy sqlc.QueueClaimLegacyBindingCapRow
		legacy, err = q.QueueClaimLegacyBindingCap(ctx, tx, sqlc.QueueClaimLegacyBindingCapParams{AppID: appID, QueueName: queueName})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, pgtype.UUID{}, "", err
		}
		if err == nil {
			cap, bindingID = legacy.MaxConcurrency, legacy.ID
		}
	}
	// Match mutation lock order: binding before trigger. Recheck the live
	// trigger under a share lock after obtaining the binding cap; a cached
	// trigger cannot claim after disable, rename, deletion, or adoption.
	_, err = q.QueueClaimLockLiveConsumer(ctx, tx, sqlc.QueueClaimLockLiveConsumerParams{ID: triggerID, AppID: appID,
		QueueName: queueName, BindingID: identity.QueueBindingID})
	if err != nil {
		return 0, pgtype.UUID{}, "", mapErr(err)
	}
	return int(cap), bindingID, identity.QueueBindingScope, nil
}

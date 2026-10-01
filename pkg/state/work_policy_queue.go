package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ClaimQueueTriggerInvocation is the named queue poller's claim path. It
// shares the work-lane and fairness locks with the generic dispatcher, then
// takes the queue binding lock used to enforce its consumer concurrency cap.
// The trigger receipt remains responsible for delivery retries and batching.
func (s *PgStore) ClaimQueueTriggerInvocation(ctx context.Context, id, triggerID, appID, queueName string, leaseSeconds int) (Invocation, error) {
	if queueName == "" || leaseSeconds <= 0 {
		return Invocation{}, ErrInvalidArgument
	}
	var appUUID, triggerUUID pgtype.UUID
	for _, item := range []struct {
		value string
		out   *pgtype.UUID
	}{{id, nil}, {appID, &appUUID}, {triggerID, &triggerUUID}} {
		parsed, err := uuid.Parse(item.value)
		if err != nil {
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
	defer func() { _ = tx.Rollback(ctx) }()
	var policyName *string
	var keyDigest, fairnessDigest []byte
	var fairnessLimit *int
	err = tx.QueryRow(ctx, `select work_policy_name, work_key_digest,
		work_fairness_digest, work_fairness_limit from invocations
		where id = $1 and app_id = $2 and source = 'queue'`, id, appID).Scan(
		&policyName, &keyDigest, &fairnessDigest, &fairnessLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrNotFound
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim lookup: %w", err)
	}
	if policyName != nil {
		if err := lockKeyedClaimTx(ctx, tx, id, appID, *policyName, keyDigest); err != nil {
			return Invocation{}, err
		}
		if fairnessLimit != nil {
			if err := lockFairnessClaimTx(ctx, tx, appID, *policyName, fairnessDigest, *fairnessLimit); err != nil {
				return Invocation{}, err
			}
		}
	}
	// Candidate enumeration is intentionally lock-free. Skip a row already
	// being claimed by another poller instead of holding the binding lock
	// while waiting for that claim to finish.
	var lockedID string
	err = tx.QueryRow(ctx, `select id::text from invocations
		where id = $1 and app_id = $2 and state = 'pending'
		for update skip locked`, id, appID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrNotFound
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger row lock: %w", err)
	}
	maxConcurrency, err := queueConsumerClaimCapTx(ctx, tx, appUUID, triggerUUID, queueName)
	if err != nil {
		return Invocation{}, err
	}
	if maxConcurrency > 0 {
		var active int
		if err := tx.QueryRow(ctx, `select count(*) from invocations
			where app_id = $1 and source = 'queue' and queue_name = $2
			  and state = 'dispatching' and lease_expires_at > clock_timestamp()`,
			appID, queueName).Scan(&active); err != nil {
			return Invocation{}, fmt.Errorf("state: queue trigger active count: %w", err)
		}
		if active >= maxConcurrency {
			return Invocation{}, ErrQuotaExceeded
		}
	}
	lease := strconv.Itoa(leaseSeconds) + " seconds"
	row := tx.QueryRow(ctx, `update invocations i set state = 'dispatching',
		lease_expires_at = clock_timestamp() + $5::interval,
		received_at = coalesce(i.received_at, clock_timestamp()),
		attempts = i.attempts + 1
		where i.id = $1 and i.app_id = $2 and i.source = 'queue'
		  and i.state = 'pending' and i.due_at <= clock_timestamp()
		  and (i.queue_name = $3 or (i.queue_name = ''
		      and i.work_policy_name is null and not exists (
		      select 1 from triggers other where other.app_id = $2
		        and other.kind = 'queue' and other.enabled and other.source = 'queue'
		        and other.id <> $4)))
		  and not exists (select 1 from trigger_records tr
		      where tr.trigger_id = $4 and tr.item_identifier = i.id::text
		        and not ((tr.state in ('pending','retry') and tr.next_fire_at <= clock_timestamp())
		          or (tr.state = 'claimed' and tr.claim_expires_at <= clock_timestamp())))
		returning `+invocationSelectCols, id, appID, queueName, triggerID, lease)
	claimed, err := scanInvocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrNotFound
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("state: queue trigger claim commit: %w", err)
	}
	return claimed, nil
}

func queueConsumerClaimCapTx(ctx context.Context, tx pgx.Tx, appID, triggerID pgtype.UUID, queueName string) (int, error) {
	q := sqlc.New()
	identity, err := q.QueueClaimConsumerIdentity(ctx, tx, sqlc.QueueClaimConsumerIdentityParams{ID: triggerID, AppID: appID})
	if err != nil {
		return 0, mapErr(err)
	}
	var cap int32
	if identity.QueueBindingID.Valid {
		cap, err = q.QueueClaimLockBinding(ctx, tx, sqlc.QueueClaimLockBindingParams{ID: identity.QueueBindingID, AppID: appID, QueueName: queueName})
		if err != nil {
			return 0, mapErr(err)
		}
	} else {
		if identity.HasMarker {
			return 0, ErrConflict
		}
		cap, err = q.QueueClaimLegacyBindingCap(ctx, tx, sqlc.QueueClaimLegacyBindingCapParams{AppID: appID, QueueName: queueName})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
	}
	// Match mutation lock order: binding before trigger. Recheck the live
	// trigger under a share lock after obtaining the binding cap; a cached
	// trigger cannot claim after disable, rename, deletion, or adoption.
	_, err = q.QueueClaimLockLiveConsumer(ctx, tx, sqlc.QueueClaimLockLiveConsumerParams{ID: triggerID, AppID: appID,
		QueueName: queueName, BindingID: identity.QueueBindingID})
	if err != nil {
		return 0, mapErr(err)
	}
	return int(cap), nil
}

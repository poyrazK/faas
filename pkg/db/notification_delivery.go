// adr: 464 — immediate delivery and replay share renewable, fenced ownership.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrNotificationLeaseLost means delivery can no longer prove ownership.
// The handler is cancelled and the row remains available for lease recovery.
var ErrNotificationLeaseLost = errors.New("db: notification lease lost")

type notificationDelivery int

const (
	notificationCompleted notificationDelivery = iota
	notificationRetrying
	notificationSkipped
	notificationDeferred
)

func notificationLeaseMilliseconds(lease time.Duration) int64 {
	return max(1, notificationOutboxLeaseDuration(lease).Milliseconds())
}

func notificationRenewalInterval(lease time.Duration) time.Duration {
	return max(time.Millisecond, notificationOutboxLeaseDuration(lease)/3)
}

// DeliverNotificationForNode claims an immediate durable notification before
// running its handler. A busy, completed, foreign-node or backed-off row is an
// expected skip. The database payload is authoritative; the broadcast supplies
// only the row ID and channel. Legacy notifications are handled by the caller.
// Handlers finish work before returning; a separate asynchronous handoff must
// arrange its own lifetime, as scheduler prime dispatch already does.
func DeliverNotificationForNode(ctx context.Context, pool *pgxpool.Pool, consumer, nodeID string, n Notification, handler func(context.Context, Notification) error) error {
	return deliverImmediateNotification(ctx, pool, consumer, nodeID, n, notificationOutboxLease, handler)
}

func deliverImmediateNotification(ctx context.Context, pool *pgxpool.Pool, consumer, nodeID string, n Notification, lease time.Duration, handler func(context.Context, Notification) error) error {
	if pool == nil || handler == nil || strings.TrimSpace(consumer) == "" || n.OutboxID <= 0 || !IsDurableNotificationChannel(n.Channel) {
		return errors.New("db: immediate notification requires pool, handler, consumer, durable channel and outbox id")
	}
	row, err := sqlc.New().ClaimImmediateNotificationForNode(ctx, pool, sqlc.ClaimImmediateNotificationForNodeParams{
		ID: n.OutboxID, Channel: n.Channel, NodeID: strings.TrimSpace(nodeID),
		ClaimToken: consumer + ":" + uuid.NewString(), LeaseMilliseconds: notificationLeaseMilliseconds(lease),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("db: claim immediate notification %d: %w", n.OutboxID, err)
	}
	item := NotificationOutboxItem{ID: row.ID, Channel: row.Channel, Payload: row.Payload, Attempts: int(row.Attempts), ClaimToken: row.ClaimToken}
	_, err = deliverNotificationClaim(ctx, pool, item, lease, handler)
	return err
}

// RenewNotification extends a live claim using the database clock. An expired
// token cannot be resurrected, even if no replacement worker has claimed yet.
func RenewNotification(ctx context.Context, pool *pgxpool.Pool, id int64, claimToken string, lease time.Duration) error {
	if pool == nil || id <= 0 || strings.TrimSpace(claimToken) == "" {
		return errors.New("db: renew notification requires pool, id and claim token")
	}
	queryCtx, cancel := context.WithTimeout(ctx, notificationRenewalInterval(lease))
	defer cancel()
	rows, err := sqlc.New().RenewNotificationClaim(queryCtx, pool, sqlc.RenewNotificationClaimParams{
		ID: id, ClaimToken: claimToken, LeaseMilliseconds: notificationLeaseMilliseconds(lease),
	})
	if err != nil {
		return fmt.Errorf("%w: renew notification %d: %w", ErrNotificationLeaseLost, id, err)
	}
	return notificationClaimUpdated(id, rows)
}

func notificationClaimUpdated(id, rows int64) error {
	if rows == 0 {
		return fmt.Errorf("%w: notification %d", ErrNotificationLeaseLost, id)
	}
	return nil
}

func completeNotificationClaim(ctx context.Context, pool *pgxpool.Pool, id int64, token string) error {
	rows, err := sqlc.New().CompleteNotificationClaim(ctx, pool, sqlc.CompleteNotificationClaimParams{ID: id, ClaimToken: token})
	if err != nil {
		return fmt.Errorf("db: complete notification %d: %w", id, err)
	}
	return notificationClaimUpdated(id, rows)
}

func failNotificationClaim(ctx context.Context, pool *pgxpool.Pool, id int64, token string, cause error) error {
	attempts, err := sqlc.New().NotificationClaimAttempts(ctx, pool, sqlc.NotificationClaimAttemptsParams{ID: id, ClaimToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: notification %d", ErrNotificationLeaseLost, id)
	}
	if err != nil {
		return fmt.Errorf("db: fail notification lookup %d: %w", id, err)
	}
	message := "notification delivery failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	rows, err := sqlc.New().FailNotificationClaim(ctx, pool, sqlc.FailNotificationClaimParams{
		ID: id, ClaimToken: token, MaxAttempts: NotificationOutboxMaxAttempts,
		RetryMilliseconds: notificationOutboxRetryDelay(int(attempts)).Milliseconds(),
		Message:           safetext.Truncate(message, notificationFailureMessageMaxBytes),
	})
	if err != nil {
		return fmt.Errorf("db: fail notification %d: %w", id, err)
	}
	return notificationClaimUpdated(id, rows)
}

func deliverNotificationClaim(ctx context.Context, pool *pgxpool.Pool, item NotificationOutboxItem, lease time.Duration, handler func(context.Context, Notification) error) (notificationDelivery, error) {
	// Confirm the claim before starting work if the claim response was delayed.
	if err := RenewNotification(ctx, pool, item.ID, item.ClaimToken, lease); err != nil {
		return notificationSkipped, err
	}
	workCtx, stopRenewal := startNotificationRenewal(ctx, pool, item, lease)
	defer func() { _ = stopRenewal() }()
	handlerErr := handler(workCtx, Notification{Channel: item.Channel, Payload: item.Payload, OutboxID: item.ID})
	if err := stopRenewal(); err != nil {
		return notificationSkipped, errors.Join(handlerErr, err)
	}
	// Bound settlement too: a stopped heartbeat must not leave an unbounded
	// database operation running past its lease. Every mutation also checks expiry.
	settleCtx, cancel := context.WithTimeout(ctx, notificationRenewalInterval(lease))
	defer cancel()
	return settleNotificationClaim(settleCtx, pool, item, handlerErr)
}

func settleNotificationClaim(ctx context.Context, pool *pgxpool.Pool, item NotificationOutboxItem, handlerErr error) (notificationDelivery, error) {
	if errors.Is(handlerErr, ErrNotificationNotOwned) {
		rows, err := sqlc.New().ReleaseUnownedNotification(ctx, pool, sqlc.ReleaseUnownedNotificationParams{ID: item.ID, ClaimToken: item.ClaimToken})
		if err != nil {
			return notificationSkipped, fmt.Errorf("db: release unowned notification %d: %w", item.ID, err)
		}
		return notificationSkipped, notificationClaimUpdated(item.ID, rows)
	}
	if handlerErr != nil {
		var deferred *DeferredNotificationError
		if errors.As(handlerErr, &deferred) {
			delay := max(notificationOutboxRetry, min(deferred.Delay, notificationOutboxMaxRetry))
			rows, err := sqlc.New().DeferNotificationClaim(ctx, pool, sqlc.DeferNotificationClaimParams{
				ID: item.ID, ClaimToken: item.ClaimToken, DelayMilliseconds: delay.Milliseconds(),
				Message: safetext.Truncate(deferred.Error(), notificationFailureMessageMaxBytes),
			})
			if err != nil {
				return notificationSkipped, fmt.Errorf("db: defer notification %d: %w", item.ID, err)
			}
			if err := notificationClaimUpdated(item.ID, rows); err != nil {
				return notificationSkipped, err
			}
			return notificationDeferred, nil
		}
		if err := failNotificationClaim(ctx, pool, item.ID, item.ClaimToken, handlerErr); err != nil {
			return notificationSkipped, errors.Join(handlerErr, err)
		}
		return notificationRetrying, handlerErr
	}
	return notificationCompleted, completeNotificationClaim(ctx, pool, item.ID, item.ClaimToken)
}

func startNotificationRenewal(ctx context.Context, pool *pgxpool.Pool, item NotificationOutboxItem, lease time.Duration) (context.Context, func() error) {
	workCtx, cancelWork := context.WithCancelCause(ctx)
	renewCtx, cancelRenewal := context.WithCancel(workCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(notificationRenewalInterval(lease))
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				if err := RenewNotification(renewCtx, pool, item.ID, item.ClaimToken, lease); err != nil {
					if renewCtx.Err() == nil {
						cancelWork(err)
					}
					return
				}
			}
		}
	}()
	return workCtx, func() error {
		cancelRenewal()
		<-done
		cause := context.Cause(workCtx)
		cancelWork(context.Canceled)
		return cause
	}
}

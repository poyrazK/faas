package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	NotificationOutboxMaxAttempts = 12
	notificationOutboxLease       = 30 * time.Second
	notificationOutboxRetry       = 5 * time.Second
	notificationOutboxMaxRetry    = 5 * time.Minute
	notificationOutboxPoll        = 2 * time.Second
	notificationOutboxBatch       = 32
	notificationOutboxRetention   = 90 * 24 * time.Hour
	notificationOutboxPrune       = 24 * time.Hour
	// Give the LISTEN fast path time to acknowledge a row before replay
	// considers it. This keeps the normal path at-most-once while preserving
	// recovery after a missed notification.
	notificationOutboxWakeDelay = 5 * time.Second
)

// ErrNotificationOutboxEmpty means there is currently no eligible row for a
// consumer. It is not a delivery failure and should not be logged as one.
var ErrNotificationOutboxEmpty = errors.New("db: notification outbox empty")

// NotificationOutboxItem is a claimed durable handoff.
type NotificationOutboxItem struct {
	ID       int64
	Channel  string
	Payload  string
	Attempts int
}

// IsDurableNotificationChannel reports whether a notification is backed by
// the replay queue. Keep this list deliberately narrow: most pg_notify users
// are advisory cache invalidations and do not need durable delivery.
func IsDurableNotificationChannel(channel string) bool {
	switch channel {
	case NotifySnapshotPrime, NotifySnapshotBoot, NotifySnapshotWritten, NotifyDeploymentReady:
		return true
	default:
		return false
	}
}

func notificationOutboxLeaseDuration(lease time.Duration) time.Duration {
	if lease <= 0 {
		return notificationOutboxLease
	}
	return lease
}

func notificationOutboxRetryDelay(attempts int) time.Duration {
	delay := notificationOutboxRetry
	for i := 1; i < attempts; i++ {
		if delay >= notificationOutboxMaxRetry/2 {
			return notificationOutboxMaxRetry
		}
		delay *= 2
	}
	if delay > notificationOutboxMaxRetry {
		return notificationOutboxMaxRetry
	}
	return delay
}

// ClaimNotification atomically leases the oldest eligible row for a consumer.
// Expired leases are reclaimed so a process killed during delivery cannot
// strand the handoff indefinitely.
func ClaimNotification(ctx context.Context, pool *pgxpool.Pool, consumer string, channels []string, lease time.Duration) (NotificationOutboxItem, error) {
	if pool == nil {
		return NotificationOutboxItem{}, errors.New("db: claim notification: nil pool")
	}
	if strings.TrimSpace(consumer) == "" {
		return NotificationOutboxItem{}, errors.New("db: claim notification: consumer required")
	}
	if len(channels) == 0 {
		return NotificationOutboxItem{}, errors.New("db: claim notification: channels required")
	}
	lease = notificationOutboxLeaseDuration(lease)
	leaseUntil := time.Now().UTC().Add(lease)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return NotificationOutboxItem{}, fmt.Errorf("db: claim notification begin: %w", err)
	}
	defer tx.Rollback(ctx) // harmless after commit

	var item NotificationOutboxItem
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			  FROM notification_outbox
			 WHERE channel = ANY($1::text[])
			   AND (
					(state = 'pending' AND available_at <= now())
					OR (state = 'processing' AND lease_until < now())
				   )
			 ORDER BY id
			 FOR UPDATE SKIP LOCKED
			 LIMIT 1
		)
		UPDATE notification_outbox o
			   SET state = 'processing',
			       attempts = o.attempts + 1,
			       claimed_by = $2,
			       claimed_at = now(),
			       lease_until = $3
		  FROM candidate
		 WHERE o.id = candidate.id
		RETURNING o.id, o.channel, o.payload, o.attempts`,
		channels, consumer, leaseUntil).Scan(&item.ID, &item.Channel, &item.Payload, &item.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationOutboxItem{}, ErrNotificationOutboxEmpty
	}
	if err != nil {
		return NotificationOutboxItem{}, fmt.Errorf("db: claim notification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return NotificationOutboxItem{}, fmt.Errorf("db: claim notification commit: %w", err)
	}
	return item, nil
}

// CompleteNotification marks a claimed row delivered. The consumer predicate
// prevents an old worker from completing a row after its lease was reclaimed.
// A row already acknowledged by the LISTEN fast path is intentionally a
// successful no-op.
func CompleteNotification(ctx context.Context, pool *pgxpool.Pool, id int64, consumer string) error {
	if pool == nil {
		return errors.New("db: complete notification: nil pool")
	}
	if id <= 0 || strings.TrimSpace(consumer) == "" {
		return errors.New("db: complete notification: invalid id or consumer")
	}
	_, err := pool.Exec(ctx, `
		UPDATE notification_outbox
		   SET state = 'delivered', delivered_at = now(),
		       claimed_by = NULL, claimed_at = NULL, lease_until = NULL,
		       last_error = NULL
		 WHERE id = $1 AND state = 'processing' AND claimed_by = $2`, id, consumer)
	if err != nil {
		return fmt.Errorf("db: complete notification %d: %w", id, err)
	}
	return nil
}

// AcknowledgeNotification closes the fast path's outbox row after a consumer
// has handed the notification to its idempotent handler. It is intentionally
// consumer-agnostic: a LISTEN delivery can race a replay worker's claim, and
// the notification itself is the proof that this daemon received the row.
func AcknowledgeNotification(ctx context.Context, pool *pgxpool.Pool, n Notification) error {
	if pool == nil || n.OutboxID <= 0 {
		return nil
	}
	_, err := pool.Exec(ctx, `
		UPDATE notification_outbox
		   SET state = 'delivered', delivered_at = now(),
		       claimed_by = NULL, claimed_at = NULL, lease_until = NULL,
		       last_error = NULL
		 WHERE id = $1 AND state IN ('pending', 'processing')`, n.OutboxID)
	if err != nil {
		return fmt.Errorf("db: acknowledge notification %d: %w", n.OutboxID, err)
	}
	return nil
}

// FailNotification releases a claimed row for retry, or dead-letters it once
// the bounded attempt budget is exhausted.
func FailNotification(ctx context.Context, pool *pgxpool.Pool, id int64, consumer string, cause error) error {
	if pool == nil {
		return errors.New("db: fail notification: nil pool")
	}
	if id <= 0 || strings.TrimSpace(consumer) == "" {
		return errors.New("db: fail notification: invalid id or consumer")
	}
	message := "notification delivery failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM notification_outbox WHERE id = $1 AND state = 'processing' AND claimed_by = $2`, id, consumer).Scan(&attempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // fast path or a newer lease already settled the row
		}
		return fmt.Errorf("db: fail notification lookup %d: %w", id, err)
	}
	next := time.Now().UTC().Add(notificationOutboxRetryDelay(attempts))
	_, err := pool.Exec(ctx, `
		UPDATE notification_outbox
		   SET state = CASE WHEN attempts >= $3 THEN 'dead_letter' ELSE 'pending' END,
		       available_at = $4,
		       claimed_by = NULL, claimed_at = NULL, lease_until = NULL,
		       last_error = $5
		 WHERE id = $1 AND state = 'processing' AND claimed_by = $2`,
		id, consumer, NotificationOutboxMaxAttempts, next, message)
	if err != nil {
		return fmt.Errorf("db: fail notification %d: %w", id, err)
	}
	return nil
}

// PruneNotifications removes terminal rows beyond the retention window.
func PruneNotifications(ctx context.Context, pool *pgxpool.Pool, before time.Time) (int64, error) {
	if pool == nil {
		return 0, errors.New("db: prune notifications: nil pool")
	}
	result, err := pool.Exec(ctx, `
		DELETE FROM notification_outbox
		 WHERE state IN ('delivered', 'dead_letter') AND created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("db: prune notifications: %w", err)
	}
	return result.RowsAffected(), nil
}

// DrainNotificationOutboxOnce replays one bounded batch. The handler is
// deliberately void-returning because the existing daemon notification APIs
// already own their retry/idempotency decisions; database claim/lease errors
// still surface to the caller.
func DrainNotificationOutboxOnce(ctx context.Context, pool *pgxpool.Pool, consumer string, channels []string, handler func(context.Context, Notification), log *slog.Logger) (int, error) {
	delivered := 0
	for i := 0; i < notificationOutboxBatch; i++ {
		item, err := ClaimNotification(ctx, pool, consumer, channels, notificationOutboxLease)
		if errors.Is(err, ErrNotificationOutboxEmpty) {
			return delivered, nil
		}
		if err != nil {
			return delivered, err
		}
		if handler != nil {
			handler(ctx, Notification{Channel: item.Channel, Payload: item.Payload, OutboxID: item.ID})
		}
		if err := CompleteNotification(ctx, pool, item.ID, consumer); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

// RunNotificationOutbox runs the bounded replay loop used by schedd and
// imaged. It intentionally waits for the first poll interval so the ordinary
// LISTEN delivery can acknowledge freshly-created rows before replay begins.
func RunNotificationOutbox(ctx context.Context, pool *pgxpool.Pool, consumer string, channels []string, handler func(context.Context, Notification), log *slog.Logger) error {
	if pool == nil {
		return errors.New("db: run notification outbox: nil pool")
	}
	ticker := time.NewTicker(notificationOutboxPoll)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := DrainNotificationOutboxOnce(ctx, pool, consumer, channels, handler, log); err != nil && ctx.Err() == nil && log != nil {
				log.Warn("db: durable notification outbox drain failed", "consumer", consumer, "err", err)
			}
			now := time.Now().UTC()
			if lastPrune.IsZero() || now.Sub(lastPrune) >= notificationOutboxPrune {
				if _, err := PruneNotifications(ctx, pool, now.Add(-notificationOutboxRetention)); err != nil && ctx.Err() == nil && log != nil {
					log.Warn("db: durable notification outbox prune failed", "consumer", consumer, "err", err)
				}
				lastPrune = now
			}
		}
	}
}

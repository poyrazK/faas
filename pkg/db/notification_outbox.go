package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// notificationFailureMessageMaxBytes bounds the recorded delivery failure
// written to notification_outbox.last_error. The message is an err.Error()
// string carrying arbitrary upstream bytes, so it is truncated with
// safetext.Truncate: a byte slice could split a rune and the resulting
// invalid UTF-8 is rejected by the text column (SQLSTATE 22021).
const notificationFailureMessageMaxBytes = 2048

const (
	NotificationOutboxMaxAttempts = 12
	notificationOutboxLease       = 30 * time.Second
	notificationOutboxRetry       = 5 * time.Second
	notificationOutboxMaxRetry    = 5 * time.Minute
	notificationOutboxPoll        = 2 * time.Second
	notificationOutboxBatch       = 32
	notificationOutboxRetention   = 90 * 24 * time.Hour
	notificationOutboxPrune       = 24 * time.Hour
	// Give immediate subscribers time to claim a new row before replay.
	// Renewable claims, rather than this grace period, prevent overlap.
	notificationOutboxWakeDelay = 5 * time.Second
)

// ErrNotificationOutboxEmpty means there is currently no eligible row for a
// consumer. It is not a delivery failure and should not be logged as one.
var ErrNotificationOutboxEmpty = errors.New("db: notification outbox empty")

// ErrRuntimeConfigRestartNotFound means no durable restart handoff exists for
// the requested app and wake ID.
var ErrRuntimeConfigRestartNotFound = errors.New("db: runtime config restart not found")

// RuntimeConfigRestartStatus is the durable projection of the restart's
// notification-outbox row. It deliberately contains only bounded status and
// error text; callers decide which fields are safe for their surface.
type RuntimeConfigRestartStatus struct {
	State       string
	Attempts    int
	LastError   string
	RequestedAt time.Time
	CompletedAt *time.Time
}

// NotificationOutboxItem is a claimed durable handoff.
type NotificationOutboxItem struct {
	ID         int64
	Channel    string
	Payload    string
	Attempts   int
	ClaimToken string
}

// IsDurableNotificationChannel reports whether a notification is backed by
// the replay queue. Keep this list deliberately narrow: most pg_notify users
// are advisory cache invalidations and do not need durable delivery.
func IsDurableNotificationChannel(channel string) bool {
	switch channel {
	case NotifyAppWake, NotifyRuntimeConfigRestart, NotifyPrivateNetworkAttachmentChanged, NotifyPrivateNetworkChanged, NotifySnapshotPrime, NotifySnapshotBoot, NotifySnapshotWritten, NotifyDeploymentReady, NotifyAppTaskChanged, NotifyEnvironmentWorkloadImage, NotifyEnvironmentWorkloadQualify:
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

// NotificationRetryDelay shares the existing bounded outbox backoff with
// durable handler progress. The recorded time is an eligibility boundary;
// polling, other work and consumer backoff can deliver a notification later.
func NotificationRetryDelay(attempts int) time.Duration {
	return notificationOutboxRetryDelay(attempts)
}

// ClaimNotification atomically leases the oldest eligible row for a consumer.
// Expired leases are reclaimed so a process killed during delivery cannot
// strand the handoff indefinitely.
func ClaimNotification(ctx context.Context, pool *pgxpool.Pool, consumer string, channels []string, lease time.Duration) (NotificationOutboxItem, error) {
	return ClaimNotificationForNode(ctx, pool, consumer, "", channels, lease)
}

// ClaimNotificationForNode skips sibling-node work before leasing it or
// incrementing attempts. An empty node retains the legacy single-box contract.
func ClaimNotificationForNode(ctx context.Context, pool *pgxpool.Pool, consumer, nodeID string, channels []string, lease time.Duration) (NotificationOutboxItem, error) {
	if pool == nil {
		return NotificationOutboxItem{}, errors.New("db: claim notification: nil pool")
	}
	if strings.TrimSpace(consumer) == "" {
		return NotificationOutboxItem{}, errors.New("db: claim notification: consumer required")
	}
	if len(channels) == 0 {
		return NotificationOutboxItem{}, errors.New("db: claim notification: channels required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return NotificationOutboxItem{}, fmt.Errorf("db: claim notification begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // harmless after commit

	claimToken := consumer + ":" + uuid.NewString()
	row, err := sqlc.New().ClaimNotificationForNode(ctx, tx, sqlc.ClaimNotificationForNodeParams{
		Channels: channels, NodeID: strings.TrimSpace(nodeID), ClaimToken: claimToken,
		LeaseMilliseconds: notificationLeaseMilliseconds(lease),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationOutboxItem{}, ErrNotificationOutboxEmpty
	}
	if err != nil {
		return NotificationOutboxItem{}, fmt.Errorf("db: claim notification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return NotificationOutboxItem{}, fmt.Errorf("db: claim notification commit: %w", err)
	}
	return NotificationOutboxItem{ID: row.ID, Channel: row.Channel, Payload: row.Payload, Attempts: int(row.Attempts), ClaimToken: row.ClaimToken}, nil
}

// CompleteNotification marks a claimed row delivered. The claim-token
// predicate prevents an expired worker from completing a row. For compatibility
// this low-level API treats an already-settled or lost claim as a no-op; the
// delivery runner uses strict completion and does not count it as delivered.
func CompleteNotification(ctx context.Context, pool *pgxpool.Pool, id int64, claimToken string) error {
	if pool == nil {
		return errors.New("db: complete notification: nil pool")
	}
	if id <= 0 || strings.TrimSpace(claimToken) == "" {
		return errors.New("db: complete notification: invalid id or claim token")
	}
	if err := completeNotificationClaim(ctx, pool, id, claimToken); !errors.Is(err, ErrNotificationLeaseLost) {
		return err
	}
	return nil
}

// GetRuntimeConfigRestartStatus returns the outbox state associated with one
// app-scoped wake ID. The wake ID is the public correlation token returned by
// POST /apps/{slug}/restart?fresh=true; appID is also required to prevent a
// guessed token from crossing tenant boundaries.
func GetRuntimeConfigRestartStatus(ctx context.Context, pool *pgxpool.Pool, appID, wakeID string) (RuntimeConfigRestartStatus, error) {
	if pool == nil {
		return RuntimeConfigRestartStatus{}, errors.New("db: runtime config restart status: nil pool")
	}
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(wakeID) == "" {
		return RuntimeConfigRestartStatus{}, errors.New("db: runtime config restart status: app id and wake id are required")
	}
	var (
		status      RuntimeConfigRestartStatus
		completedAt pgtype.Timestamptz
	)
	err := pool.QueryRow(ctx, `
		SELECT state, attempts, COALESCE(last_error, ''), created_at, delivered_at
		  FROM notification_outbox
		 WHERE channel = $1
		   AND payload::jsonb ->> 'app_id' = $2
		   AND payload::jsonb ->> 'wake_id' = $3
		 ORDER BY id DESC
		 LIMIT 1`, NotifyRuntimeConfigRestart, appID, wakeID).Scan(
		&status.State, &status.Attempts, &status.LastError, &status.RequestedAt, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfigRestartStatus{}, ErrRuntimeConfigRestartNotFound
	}
	if err != nil {
		return RuntimeConfigRestartStatus{}, fmt.Errorf("db: runtime config restart status: %w", err)
	}
	if completedAt.Valid {
		completed := completedAt.Time
		status.CompletedAt = &completed
	}
	return status, nil
}

// AcknowledgeNotification supports legacy subscribers handling pending rows.
// It cannot close an active claim; that requires its token. Renewable delivery
// consumers use DeliverNotificationForNode instead.
func AcknowledgeNotification(ctx context.Context, pool *pgxpool.Pool, n Notification) error {
	if pool == nil || n.OutboxID <= 0 {
		return nil
	}
	err := sqlc.New().AcknowledgePendingNotification(ctx, pool, n.OutboxID)
	if err != nil {
		return fmt.Errorf("db: acknowledge notification %d: %w", n.OutboxID, err)
	}
	return nil
}

// FailNotification releases a claimed row for retry, or dead-letters it once
// the bounded attempt budget is exhausted. The claim token ensures an expired
// worker cannot reset a newer worker's lease.
func FailNotification(ctx context.Context, pool *pgxpool.Pool, id int64, claimToken string, cause error) error {
	if pool == nil {
		return errors.New("db: fail notification: nil pool")
	}
	if id <= 0 || strings.TrimSpace(claimToken) == "" {
		return errors.New("db: fail notification: invalid id or claim token")
	}
	if err := failNotificationClaim(ctx, pool, id, claimToken, cause); !errors.Is(err, ErrNotificationLeaseLost) {
		return err
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

// DrainNotificationOutboxOnce replays one bounded batch. Handler failures leave
// the row pending with the normal outbox backoff so accepted scheduler work is
// never acknowledged before it has completed.
func DrainNotificationOutboxOnce(ctx context.Context, pool *pgxpool.Pool, consumer string, channels []string, handler func(context.Context, Notification) error, log *slog.Logger) (int, error) {
	return DrainNotificationOutboxOnceForNode(ctx, pool, consumer, "", channels, handler, log)
}

// DrainNotificationOutboxOnceForNode replays shared work and local-node
// handoffs. A skipped callback is released without spending a retry attempt.
func DrainNotificationOutboxOnceForNode(ctx context.Context, pool *pgxpool.Pool, consumer, nodeID string, channels []string, handler func(context.Context, Notification) error, log *slog.Logger) (int, error) {
	if handler == nil {
		return 0, errors.New("db: notification handler required")
	}
	delivered := 0
	for i := 0; i < notificationOutboxBatch; i++ {
		item, err := ClaimNotificationForNode(ctx, pool, consumer, nodeID, channels, notificationOutboxLease)
		if errors.Is(err, ErrNotificationOutboxEmpty) {
			return delivered, nil
		}
		if err != nil {
			return delivered, err
		}
		outcome, err := deliverNotificationClaim(ctx, pool, item, notificationOutboxLease, handler)
		if outcome == notificationRetrying {
			if log != nil {
				log.Warn("db: durable notification delivery failed; queued for retry",
					"consumer", consumer, "channel", item.Channel, "id", item.ID, "err", err)
			}
			continue
		}
		if err != nil {
			return delivered, err
		}
		if outcome == notificationSkipped {
			return delivered, nil // do not reclaim the same unscoped skip
		}
		delivered++
	}
	return delivered, nil
}

// RunNotificationOutbox runs the bounded replay loop used by schedd and
// imaged. It intentionally waits for the first poll interval so the ordinary
// LISTEN delivery can claim freshly-created rows before replay begins.
func RunNotificationOutbox(ctx context.Context, pool *pgxpool.Pool, consumer string, channels []string, handler func(context.Context, Notification) error, log *slog.Logger) error {
	return RunNotificationOutboxForNode(ctx, pool, consumer, "", channels, handler, log)
}

// RunNotificationOutboxForNode retains the normal poll/retry lifecycle while
// limiting node-local handoffs to their owner.
func RunNotificationOutboxForNode(ctx context.Context, pool *pgxpool.Pool, consumer, nodeID string, channels []string, handler func(context.Context, Notification) error, log *slog.Logger) error {
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
			if _, err := DrainNotificationOutboxOnceForNode(ctx, pool, consumer, nodeID, channels, handler, log); err != nil && ctx.Err() == nil && log != nil {
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

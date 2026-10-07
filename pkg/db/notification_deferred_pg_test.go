package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNotificationDeferredPreservesRetryBudgetAndStorageBackoff(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	n := insertDeliveryNotification(t, ctx, pool)
	for attempt := 0; attempt < NotificationOutboxMaxAttempts+2; attempt++ {
		if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=now()-interval '1 second' WHERE id=$1`, n.OutboxID); err != nil {
			t.Fatal(err)
		}
		if err := DeliverNotificationForNode(ctx, pool, "paused-scheduler", "node-a", n, func(context.Context, Notification) error { return ErrNotificationDeferred }); err != nil {
			t.Fatal(err)
		}
		var state, message string
		var attempts int
		var future bool
		if err := pool.QueryRow(ctx, `SELECT state,attempts,last_error,available_at>clock_timestamp(),claimed_by IS NULL AND lease_until IS NULL FROM notification_outbox WHERE id=$1`, n.OutboxID).Scan(&state, &attempts, &message, &future, new(bool)); err != nil {
			t.Fatal(err)
		}
		if state != "pending" || attempts != 0 || message != "application_standard_refresh_deferred" || !future {
			t.Fatal("pause spent attempt budget or lost durable work", state, attempts, message, future)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=now()-interval '1 second' WHERE id=$1`, n.OutboxID); err != nil {
		t.Fatal(err)
	}
	if err := DeliverNotificationForNode(ctx, pool, "resumed-scheduler", "node-a", n, func(context.Context, Notification) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM notification_outbox WHERE id=$1`, n.OutboxID).Scan(&state); err != nil || state != "delivered" {
		t.Fatal("resumed handoff did not complete", state, err)
	}
}

func TestNotificationDeferredCannotSettleExpiredClaim(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	n := insertDeliveryNotification(t, ctx, pool)
	item, err := ClaimNotificationForNode(ctx, pool, "old-scheduler", "node-a", []string{n.Channel}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := settleNotificationClaim(ctx, pool, item, ErrNotificationDeferred); !errors.Is(err, ErrNotificationLeaseLost) {
		t.Fatal("expired token deferred work", err)
	}
}

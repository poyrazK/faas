// adr: 685
package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

func TestDeferredNotificationDoesNotExhaustDeliveryBudget(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	n := insertDeliveryNotification(t, ctx, pool)
	for i := 0; i < NotificationOutboxMaxAttempts+2; i++ {
		if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=now() WHERE id=$1`, n.OutboxID); err != nil {
			t.Fatal(err)
		}
		_, err := DrainNotificationOutboxOnce(ctx, pool, "dependency-wait", []string{n.Channel}, func(context.Context, Notification) error {
			return &DeferredNotificationError{Cause: fmt.Errorf("waiting for dependency release"), Delay: api.ProjectDependencyGatePoll}
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var status, blocker string
		var attempts int
		var available time.Time
		if err := pool.QueryRow(ctx, `SELECT state, attempts, available_at, coalesce(last_error,'') FROM notification_outbox WHERE id=$1`, n.OutboxID).Scan(&status, &attempts, &available, &blocker); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || attempts != 0 || !available.After(time.Now()) || blocker == "" {
			t.Fatalf("deferral spent failure budget: state=%s attempts=%d available=%v blocker=%q", status, attempts, available, blocker)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=now() WHERE id=$1`, n.OutboxID); err != nil {
		t.Fatal(err)
	}
	delivered, err := DrainNotificationOutboxOnce(ctx, pool, "restarted", []string{n.Channel}, func(context.Context, Notification) error { return nil }, nil)
	if err != nil || delivered != 1 {
		t.Fatalf("recovery delivery = %d, %v", delivered, err)
	}
}

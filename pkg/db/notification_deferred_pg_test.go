// adr: 646
package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

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

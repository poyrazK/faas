package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func notificationOutboxPG(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool, ctx
}

func TestDrainNotificationOutboxOnceFailureRemainsReplayable(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)

	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO notification_outbox (channel, payload, available_at)
		VALUES ($1, $2, now() - interval '1 second')
		RETURNING id`, NotifySnapshotBoot, `{"deployment_id":"dep-1"}`).Scan(&id); err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}

	calls := 0
	delivered, err := DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{NotifySnapshotBoot}, func(_ context.Context, n Notification) error {
		calls++
		if n.OutboxID != id {
			t.Fatalf("handler outbox id = %d, want %d", n.OutboxID, id)
		}
		return errors.New("snapshot backend temporarily unavailable")
	}, nil)
	if err != nil {
		t.Fatalf("failed drain: %v", err)
	}
	if delivered != 0 || calls != 1 {
		t.Fatalf("failed drain = delivered %d, calls %d; want 0, 1", delivered, calls)
	}

	var state string
	var attempts int
	var lastError string
	var availableAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT state, attempts, COALESCE(last_error, ''), available_at
		  FROM notification_outbox WHERE id = $1`, id).Scan(&state, &attempts, &lastError, &availableAt); err != nil {
		t.Fatalf("inspect failed row: %v", err)
	}
	if state != "pending" || attempts != 1 || lastError != "snapshot backend temporarily unavailable" {
		t.Fatalf("failed row = state %q attempts %d error %q; want pending, 1, recorded error", state, attempts, lastError)
	}
	if !availableAt.After(time.Now().UTC()) {
		t.Fatalf("retry available_at = %v, want future backoff", availableAt)
	}

	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	delivered, err = DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{NotifySnapshotBoot}, func(_ context.Context, n Notification) error {
		calls++
		if n.OutboxID != id {
			t.Fatalf("retry handler outbox id = %d, want %d", n.OutboxID, id)
		}
		return nil
	}, nil)
	if err != nil || delivered != 1 || calls != 2 {
		t.Fatalf("successful retry = delivered %d, calls %d, err %v; want 1, 2, nil", delivered, calls, err)
	}

	var deliveredState string
	var deliveredAt bool
	if err := pool.QueryRow(ctx, `
		SELECT state, delivered_at IS NOT NULL
		  FROM notification_outbox WHERE id = $1`, id).Scan(&deliveredState, &deliveredAt); err != nil {
		t.Fatalf("inspect delivered row: %v", err)
	}
	if deliveredState != "delivered" || !deliveredAt {
		t.Fatalf("delivered row = state %q delivered_at_set %v; want delivered, true", deliveredState, deliveredAt)
	}
}

func TestClaimNotificationReclaimsLeaseAndFencesStaleWorker(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	staleToken := "imaged:stale-worker"

	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO notification_outbox (
			channel, payload, state, attempts, claimed_by, claimed_at, lease_until
		) VALUES ($1, $2, 'processing', 1, $3, now() - interval '1 minute', now() - interval '1 second')
		RETURNING id`, NotifySnapshotWritten, `{"deployment_id":"dep-2"}`, staleToken).Scan(&id); err != nil {
		t.Fatalf("insert expired outbox row: %v", err)
	}

	item, err := ClaimNotification(ctx, pool, "imaged", []string{NotifySnapshotWritten}, time.Minute)
	if err != nil {
		t.Fatalf("reclaim expired row: %v", err)
	}
	if item.ID != id || item.Attempts != 2 || item.ClaimToken == "" || item.ClaimToken == staleToken {
		t.Fatalf("reclaimed item = %+v; want id %d, attempts 2, fresh claim token", item, id)
	}

	if err := CompleteNotification(ctx, pool, id, staleToken); err != nil {
		t.Fatalf("stale completion: %v", err)
	}
	if err := FailNotification(ctx, pool, id, staleToken, errors.New("stale worker failed")); err != nil {
		t.Fatalf("stale failure: %v", err)
	}

	var state string
	var claimedBy string
	if err := pool.QueryRow(ctx, `SELECT state, claimed_by FROM notification_outbox WHERE id = $1`, id).Scan(&state, &claimedBy); err != nil {
		t.Fatalf("inspect reclaimed row: %v", err)
	}
	if state != "processing" || claimedBy != item.ClaimToken {
		t.Fatalf("stale worker changed row = state %q claimed_by %q; want processing, %q", state, claimedBy, item.ClaimToken)
	}

	if err := CompleteNotification(ctx, pool, id, item.ClaimToken); err != nil {
		t.Fatalf("current completion: %v", err)
	}
}

func TestAcknowledgeNotificationPreventsOutboxReplay(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)

	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO notification_outbox (channel, payload, available_at)
		VALUES ($1, $2, now() - interval '1 second')
		RETURNING id`, NotifyDeploymentReady, `{"deployment_id":"dep-3"}`).Scan(&id); err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}
	if err := AcknowledgeNotification(ctx, pool, Notification{OutboxID: id}); err != nil {
		t.Fatalf("acknowledge fast-path notification: %v", err)
	}

	calls := 0
	delivered, err := DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{NotifyDeploymentReady}, func(context.Context, Notification) error {
		calls++
		return nil
	}, nil)
	if err != nil || delivered != 0 || calls != 0 {
		t.Fatalf("replay after fast-path ack = delivered %d, calls %d, err %v; want 0, 0, nil", delivered, calls, err)
	}
}

// adr: 464 — renewable delivery against real PostgreSQL, with competing workers.
package db

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func insertDeliveryNotification(t *testing.T, ctx context.Context, pool *pgxpool.Pool) Notification {
	t.Helper()
	n := Notification{Channel: NotifySnapshotBoot, Payload: `{"node_id":"node-a","deployment_id":"stored"}`}
	if err := pool.QueryRow(ctx, `INSERT INTO notification_outbox (channel, payload, available_at)
		VALUES ($1, $2, now()) RETURNING id`, n.Channel, n.Payload).Scan(&n.OutboxID); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImmediateNotificationClaimsStoredIdentityAndHonorsEligibility(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	for _, tc := range []struct {
		name, status, node, channel string
		attempts                    int
		expired, run                bool
	}{
		{name: "first delivery bypasses grace", status: "pending", node: "node-a", run: true},
		{name: "normalized owner", status: "pending", node: "\u2003node-a\t", run: true},
		{name: "retry respects backoff", status: "pending", node: "node-a", attempts: 2},
		{name: "foreign node", status: "pending", node: "node-b"},
		{name: "wrong channel", status: "pending", node: "node-a", channel: NotifySnapshotWritten},
		{name: "active claim", status: "processing", node: "node-a", attempts: 1},
		{name: "expired claim", status: "processing", node: "node-a", attempts: 1, expired: true, run: true},
		{name: "completed", status: "delivered", node: "node-a", attempts: 1},
		{name: "dead letter", status: "dead_letter", node: "node-a", attempts: NotificationOutboxMaxAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := insertDeliveryNotification(t, ctx, pool)
			if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET state=$2, attempts=$3,
				available_at=now()+interval '1 minute', claimed_by='earlier',
				lease_until=CASE WHEN $4 THEN now()-interval '1 second' ELSE now()+interval '1 minute' END
				WHERE id=$1`, n.OutboxID, tc.status, tc.attempts, tc.expired); err != nil {
				t.Fatal(err)
			}
			storedPayload := n.Payload
			n.Payload = `{"node_id":"node-b","deployment_id":"forged"}`
			if tc.channel != "" {
				n.Channel = tc.channel
			}
			calls := 0
			err := DeliverNotificationForNode(ctx, pool, "imaged", tc.node, n, func(_ context.Context, got Notification) error {
				calls++
				if got.Payload != storedPayload || got.Channel != NotifySnapshotBoot || got.OutboxID != n.OutboxID {
					t.Errorf("handler trusted broadcast identity: %+v", got)
				}
				return nil
			})
			wantCalls, wantAttempts, wantStatus := 0, tc.attempts, tc.status
			if tc.run {
				wantCalls, wantAttempts, wantStatus = 1, tc.attempts+1, "delivered"
			}
			if err != nil || calls != wantCalls {
				t.Fatalf("delivery calls=%d err=%v, want %d/nil", calls, err, wantCalls)
			}
			assertDeliveryRow(t, ctx, pool, n.OutboxID, wantStatus, wantAttempts, "")
		})
	}
}

func assertDeliveryRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, status string, attempts int, token string) {
	t.Helper()
	var gotStatus, gotToken string
	var gotAttempts int
	if err := pool.QueryRow(ctx, `SELECT state, attempts, coalesce(claimed_by,'')
		FROM notification_outbox WHERE id=$1`, id).Scan(&gotStatus, &gotAttempts, &gotToken); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status || gotAttempts != attempts || (token != "" && gotToken != token) {
		t.Fatalf("row=%s/%d/%s, want %s/%d/%s", gotStatus, gotAttempts, gotToken, status, attempts, token)
	}
}

func TestNotificationDeliveryRenewsAcrossLeasePeriods(t *testing.T) {
	for _, path := range []string{"immediate", "replay"} {
		t.Run(path, func(t *testing.T) {
			pool, _ := notificationOutboxPG(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			n := insertDeliveryNotification(t, ctx, pool)
			const lease = 900 * time.Millisecond
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			result := make(chan error, 1)
			var calls atomic.Int32
			handler := func(workCtx context.Context, _ Notification) error {
				calls.Add(1)
				close(entered)
				select {
				case <-release:
					return nil
				case <-workCtx.Done():
					return workCtx.Err()
				}
			}
			go func() {
				if path == "immediate" {
					result <- deliverImmediateNotification(ctx, pool, "imaged", "node-a", n, lease, handler)
					return
				}
				item, err := ClaimNotificationForNode(ctx, pool, "imaged", "node-a", []string{n.Channel}, lease)
				if err == nil {
					_, err = deliverNotificationClaim(ctx, pool, item, lease, handler)
				}
				result <- err
			}()
			select {
			case <-entered:
			case err := <-result:
				t.Fatalf("owner did not enter handler: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Check both immediate and replay competition throughout more than
			// two original leases, rather than checking only the final row.
			until := time.NewTimer(2*lease + 100*time.Millisecond)
			defer until.Stop()
			tick := time.NewTicker(150 * time.Millisecond)
			defer tick.Stop()
		competition:
			for {
				select {
				case <-until.C:
					break competition
				case <-tick.C:
					if item, err := ClaimNotificationForNode(ctx, pool, "competitor", "node-a", []string{n.Channel}, lease); !errors.Is(err, ErrNotificationOutboxEmpty) {
						t.Fatalf("replay stole healthy work: item=%+v err=%v", item, err)
					}
					if err := DeliverNotificationForNode(ctx, pool, "subscriber", "node-a", n, func(context.Context, Notification) error {
						calls.Add(1)
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				case err := <-result:
					t.Fatalf("healthy owner stopped: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			assertDeliveryRow(t, ctx, pool, n.OutboxID, "processing", 1, "")
			releaseOnce.Do(func() { close(release) })
			if err := <-result; err != nil || calls.Load() != 1 {
				t.Fatalf("long delivery: calls=%d err=%v", calls.Load(), err)
			}
			assertDeliveryRow(t, ctx, pool, n.OutboxID, "delivered", 1, "")
		})
	}
}

func TestNotificationDeliveryCancelsWithoutSettlingLostClaim(t *testing.T) {
	for _, loss := range []string{"replacement", "expired", "blocked renewal", "shutdown"} {
		t.Run(loss, func(t *testing.T) {
			pool, ctx := notificationOutboxPG(t)
			n := insertDeliveryNotification(t, ctx, pool)
			const lease = 900 * time.Millisecond
			item, err := ClaimNotification(ctx, pool, "imaged", []string{n.Channel}, lease)
			if err != nil {
				t.Fatal(err)
			}
			workParent, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			entered := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				_, err := deliverNotificationClaim(workParent, pool, item, lease, func(workCtx context.Context, _ Notification) error {
					close(entered)
					<-workCtx.Done()
					return nil // cancellation must prevent even an apparent success
				})
				result <- err
			}()
			select {
			case <-entered:
			case err := <-result:
				t.Fatalf("handler did not start: %v", err)
			case <-workParent.Done():
				t.Fatal(workParent.Err())
			}
			wantToken := item.ClaimToken
			switch loss {
			case "replacement":
				wantToken = "replacement-worker"
				_, err = pool.Exec(ctx, "UPDATE notification_outbox SET claimed_by=$2, lease_until=now()+interval '1 minute' WHERE id=$1", item.ID, wantToken)
			case "expired":
				_, err = pool.Exec(ctx, "UPDATE notification_outbox SET lease_until=now()-interval '1 second' WHERE id=$1", item.ID)
			case "blocked renewal":
				tx, txErr := pool.Begin(ctx)
				if txErr != nil {
					t.Fatal(txErr)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				_, err = tx.Exec(ctx, "SELECT id FROM notification_outbox WHERE id=$1 FOR UPDATE", item.ID)
			case "shutdown":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				want := ErrNotificationLeaseLost
				if loss == "shutdown" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("lost delivery returned %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("handler was not cancelled within its lease budget")
			}
			assertDeliveryRow(t, ctx, pool, item.ID, "processing", 1, wantToken)
		})
	}
}

func TestAbandonedNotificationClaimExpiresAndFencesAllStaleSettlement(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	n := insertDeliveryNotification(t, ctx, pool)
	const lease = 900 * time.Millisecond
	old, err := ClaimNotification(ctx, pool, "old-worker", []string{n.Channel}, lease)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != "worker exited" {
				t.Error("did not simulate worker exit")
			}
		}()
		_, _ = deliverNotificationClaim(ctx, pool, old, lease, func(context.Context, Notification) error { panic("worker exited") })
	}()
	// The panic cleanup must stop renewal. No row rewriting simulates expiry.
	time.Sleep(lease + 100*time.Millisecond)
	assertExpiredClaimFenced(t, pool, old)
	replacement, err := ClaimNotification(ctx, pool, "replacement", []string{n.Channel}, time.Minute)
	if err != nil || replacement.Attempts != 2 || replacement.ClaimToken == old.ClaimToken {
		t.Fatalf("abandoned claim was not recovered: %+v err=%v", replacement, err)
	}
	assertExpiredClaimFenced(t, pool, old)
	assertDeliveryRow(t, ctx, pool, old.ID, "processing", 2, replacement.ClaimToken)
	if err := CompleteNotification(ctx, pool, replacement.ID, replacement.ClaimToken); err != nil {
		t.Fatal(err)
	}
	assertDeliveryRow(t, ctx, pool, old.ID, "delivered", 2, "")
}

func assertExpiredClaimFenced(t *testing.T, pool *pgxpool.Pool, item NotificationOutboxItem) {
	t.Helper()
	ctx := context.Background()
	if err := RenewNotification(ctx, pool, item.ID, item.ClaimToken, time.Minute); !errors.Is(err, ErrNotificationLeaseLost) {
		t.Fatalf("stale renewal resurrected a claim: %v", err)
	}
	if err := completeNotificationClaim(ctx, pool, item.ID, item.ClaimToken); !errors.Is(err, ErrNotificationLeaseLost) {
		t.Fatalf("stale completion looked delivered: %v", err)
	}
	if err := FailNotification(ctx, pool, item.ID, item.ClaimToken, errors.New("stale error")); err != nil {
		t.Fatal(err)
	}
	if err := AcknowledgeNotification(ctx, pool, Notification{OutboxID: item.ID}); err != nil {
		t.Fatal(err)
	}
	if rows, err := sqlc.New().ReleaseUnownedNotification(ctx, pool, sqlc.ReleaseUnownedNotificationParams{ID: item.ID, ClaimToken: item.ClaimToken}); err != nil || rows != 0 {
		t.Fatalf("stale skip updated claim: rows=%d err=%v", rows, err)
	}
}

func TestImmediateNotificationFailureBackoffAndExhaustion(t *testing.T) {
	pool, ctx := notificationOutboxPG(t)
	for _, attempts := range []int{0, NotificationOutboxMaxAttempts - 1} {
		n := insertDeliveryNotification(t, ctx, pool)
		if _, err := pool.Exec(ctx, "UPDATE notification_outbox SET attempts=$2 WHERE id=$1", n.OutboxID, attempts); err != nil {
			t.Fatal(err)
		}
		cause := errors.New("backend temporarily unavailable")
		calls := 0
		handler := func(context.Context, Notification) error { calls++; return cause }
		if err := DeliverNotificationForNode(ctx, pool, "imaged", "node-a", n, handler); !errors.Is(err, cause) {
			t.Fatalf("failed delivery: %v", err)
		}
		status := "pending"
		if attempts+1 == NotificationOutboxMaxAttempts {
			status = "dead_letter"
		}
		assertDeliveryRow(t, ctx, pool, n.OutboxID, status, attempts+1, "")
		if err := DeliverNotificationForNode(ctx, pool, "imaged", "node-a", n, handler); err != nil || calls != 1 {
			t.Fatalf("duplicate bypassed backoff/exhaustion: calls=%d err=%v", calls, err)
		}
		var message string
		if err := pool.QueryRow(ctx, "SELECT last_error FROM notification_outbox WHERE id=$1", n.OutboxID).Scan(&message); err != nil || message != cause.Error() {
			t.Fatalf("failure receipt=%s err=%v", message, err)
		}
	}
}

func TestNotificationExpiryIsCheckedAfterWaitingForRowLock(t *testing.T) {
	pool, _ := notificationOutboxPG(t)
	for _, operation := range []string{"renew", "complete", "fail", "skip"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			n := insertDeliveryNotification(t, ctx, pool)
			// Each earlier expired row stays processing; claim by ID to isolate
			// this attempt from the previous subtest's abandoned delivery.
			row, err := sqlc.New().ClaimImmediateNotificationForNode(ctx, pool, sqlc.ClaimImmediateNotificationForNodeParams{
				ID: n.OutboxID, Channel: n.Channel, NodeID: "node-a", ClaimToken: "owner", LeaseMilliseconds: 600,
			})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, "SELECT id FROM notification_outbox WHERE id=$1 FOR UPDATE", row.ID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "renew":
					err = RenewNotification(ctx, pool, row.ID, row.ClaimToken, time.Minute)
				case "complete":
					err = completeNotificationClaim(ctx, pool, row.ID, row.ClaimToken)
				case "fail":
					err = failNotificationClaim(ctx, pool, row.ID, row.ClaimToken, errors.New("late failure"))
				case "skip":
					var rows int64
					rows, err = sqlc.New().ReleaseUnownedNotification(ctx, pool, sqlc.ReleaseUnownedNotificationParams{ID: row.ID, ClaimToken: row.ClaimToken})
					if err == nil {
						err = notificationClaimUpdated(row.ID, rows)
					}
				}
				result <- err
			}()
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(700 * time.Millisecond)
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, ErrNotificationLeaseLost) {
				t.Fatalf("%s used ownership checked before waiting: %v", operation, err)
			}
			assertDeliveryRow(t, ctx, pool, row.ID, "processing", 1, row.ClaimToken)
		})
	}
}

//go:build !no_pg

package sched_test

// adr: 435, 581. Real durable handoff and publication compose with PostgreSQL.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresApplicationStandardComposedSnapshots(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	sched.RunApplicationStandardComposedSnapshots(t, store, func(node string) sched.ApplicationStandardSnapshotTestHooks {
		h := composedSnapshotImageHandler(t, store, node)
		return sched.ApplicationStandardSnapshotTestHooks{
			Notifier: db.PoolNotifier{Pool: pool},
			Publish: func(ctx context.Context, n db.Notification) error {
				var status string
				if err := pool.QueryRow(ctx, `SELECT state FROM notification_outbox WHERE channel=$1 AND payload::jsonb=$2::jsonb ORDER BY id DESC LIMIT 1`, n.Channel, n.Payload).Scan(&status); err != nil {
					return err
				}
				if status == "delivered" {
					// Delayed advisory delivery must still pass imaged's final
					// storage fence, even after the durable work was settled.
					return h.HandleNotification(ctx, n)
				}
				// Advance only durable queue replay eligibility, as with the
				// runtime handoff; all capture and approval clocks stay real.
				if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=clock_timestamp() WHERE channel=$1 AND payload::jsonb=$2::jsonb AND state='pending'`, n.Channel, n.Payload); err != nil {
					return err
				}
				seen := 0
				_, err := db.DrainNotificationOutboxOnceForNode(ctx, pool, "composed-snapshot-imaged", node, []string{db.NotifySnapshotWritten}, func(ctx context.Context, actual db.Notification) error {
					if actual.Payload != n.Payload || actual.OutboxID == 0 {
						return fmt.Errorf("snapshot outbox lost the exact capture reference")
					}
					seen++
					return h.HandleNotification(ctx, actual)
				}, nil)
				var attempts int
				var completed bool
				if err == nil {
					err = pool.QueryRow(ctx, `SELECT state,attempts,delivered_at IS NOT NULL FROM notification_outbox WHERE channel=$1 AND payload::jsonb=$2::jsonb ORDER BY id DESC LIMIT 1`, n.Channel, n.Payload).Scan(&status, &attempts, &completed)
				}
				if err == nil && (seen != 1 || status != "delivered" || attempts != 1 || !completed) {
					return fmt.Errorf("snapshot publication not settled exactly once: seen=%d state=%s attempts=%d completed=%t", seen, status, attempts, completed)
				}
				return err
			},
			Refresh: func(engine *sched.Engine, request state.ApplicationStandardRuntimeRefreshRequest) error {
				return composedSnapshotRuntimeHandoff(t.Context(), pool, engine, node, request)
			},
		}
	})
}

func composedSnapshotRuntimeHandoff(ctx context.Context, pool *pgxpool.Pool, engine *sched.Engine, node string, r state.ApplicationStandardRuntimeRefreshRequest) error {
	before, err := db.GetRuntimeConfigRestartStatus(ctx, pool, r.AppID, r.WakeID)
	if err != nil {
		return err
	}
	// Only queue replay eligibility advances; authority and evidence clocks do not.
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=clock_timestamp() WHERE channel=$1 AND payload::jsonb->>'app_id'=$2 AND payload::jsonb->>'wake_id'=$3 AND state='pending'`, db.NotifyRuntimeConfigRestart, r.AppID, r.WakeID); err != nil {
		return err
	}
	loop := sched.NewLoop(pool, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	seen := 0
	_, err = db.DrainNotificationOutboxOnceForNode(ctx, pool, "composed-snapshot-schedd", node, []string{db.NotifyRuntimeConfigRestart}, func(ctx context.Context, n db.Notification) error {
		var actual state.ApplicationStandardRuntimeRefreshRequest
		if err := json.Unmarshal([]byte(n.Payload), &actual); err != nil {
			return err
		}
		if actual != r || n.OutboxID == 0 {
			return fmt.Errorf("runtime outbox lost its installed revision binding")
		}
		seen++
		return loop.HandleDurableNotification(ctx, n)
	}, nil)
	if err != nil {
		return err
	}
	after, err := db.GetRuntimeConfigRestartStatus(ctx, pool, r.AppID, r.WakeID)
	wantSeen := 1
	if before.State == "delivered" {
		wantSeen = 0
	}
	if err == nil && (seen != wantSeen || after.State != "delivered" || after.Attempts != 1 || after.CompletedAt == nil) {
		return fmt.Errorf("runtime handoff not settled exactly once: before=%+v after=%+v seen=%d", before, after, seen)
	}
	return err
}

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
)

// startOwnerCache subscribes to app_runtime_changed before turning the
// owner cache on (ADR-1741), so no entry is ever loaded without the
// notification that would drop it. A resync, sent after a reconnect or a
// dropped delivery, drops every entry.
func startOwnerCache(ctx context.Context, pool *pgxpool.Pool, engine *sched.Engine, log *slog.Logger) error {
	notifs, err := db.SubscribeWithResync(ctx, pool, []string{db.NotifyAppRuntimeChanged}, log)
	if err != nil {
		return fmt.Errorf("schedd: subscribe %s: %w", db.NotifyAppRuntimeChanged, err)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case n, ok := <-notifs:
				if !ok {
					return
				}
				engine.HandleOwnerCacheNotification(n)
			}
		}
	}()
	engine.WithOwnerCache()
	return nil
}

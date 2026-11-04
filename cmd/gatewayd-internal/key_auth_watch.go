package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// watchKeyChanges drops the gateway's API key cache on every key_changed
// notification (apid sends one on each key create, revoke and rotation) and
// whenever notifications may have been missed. Until it subscribes, or while
// the LISTEN connection is down, cached answers still lapse after
// api.GatewayKeyAuthCacheTTL.
func watchKeyChanges(ctx context.Context, pool *pgxpool.Pool, cache *gateway.KeyAuthCache, log *slog.Logger) {
	if pool == nil || cache == nil {
		return
	}
	notif, err := db.SubscribeWithResync(ctx, pool, []string{db.NotifyKeyChanged}, log)
	if err != nil {
		log.Error("gatewayd-internal: subscribe key_changed; key cache relies on its TTL", "err", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-notif:
			if !ok {
				return
			}
			cache.Invalidate()
		}
	}
}

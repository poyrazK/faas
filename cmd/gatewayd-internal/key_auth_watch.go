package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// watchKeyChanges applies every key_changed notification (apid sends one on
// each key create, revoke and rotation, naming the account or organization)
// to the gateway's API key cache, and marks the cache for revalidation
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
		case n, ok := <-notif:
			if !ok {
				return
			}
			applyKeyNotification(cache, n)
		}
	}
}

// applyKeyNotification maps one delivery onto the cache: a resync keeps the
// verified keys as outage fallbacks but revalidates them, a scoped change
// drops only its owner's keys, and a payload naming no owner drops all.
func applyKeyNotification(cache *gateway.KeyAuthCache, n db.Notification) {
	if n.Channel == db.ResyncChannel {
		cache.Expire()
		return
	}
	if ch, ok := gateway.ParseKeyChange(n.Payload); ok {
		cache.ApplyKeyChange(ch)
		return
	}
	cache.Invalidate()
}

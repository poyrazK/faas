package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// startPlatformFlags seeds the ai_gateway platform flag (ADR-1700) from the
// configured release gate and rollout, applies the durable operator value
// once so the daemon starts on it, and keeps the flag current in the
// background with the shared runtime-config watcher. When Postgres is
// unreachable at boot, outboundd serves on the configured fallback and the
// watcher converges once it can read the table.
func startPlatformFlags(ctx context.Context, cfg *Config, pool *pgxpool.Pool, log *slog.Logger) (*platformflags.Set, error) {
	rollout, err := cfg.AIGatewayRollout(os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("outboundd: %w", err)
	}
	set := platformflags.NewSet(map[string]platformflags.Definition{
		platformflags.AIGateway: outbound.AIGatewayFlagBootstrap(cfg.AIGateway(os.Getenv), rollout),
	})
	keys, apply := runtimeconfig.WithPlatformFlags(nil, nil, set)
	watcher := runtimeconfig.New(state.NewPgStore(pool), pool, keys, apply, log)
	watcher.Daemon = "outboundd"
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("outboundd: initial platform flag reconcile failed; serving on the configured fallback", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("outboundd: platform flag watcher stopped", "err", err)
		}
	}()
	return set, nil
}

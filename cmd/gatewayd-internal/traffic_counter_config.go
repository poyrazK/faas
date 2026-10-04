// adr: 531
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func trafficCounterMode(cfg *Config) (string, error) {
	if cfg == nil {
		return "central", nil
	}
	if err := cfg.validateTrafficMode(); err != nil {
		return "", err
	}
	return cfg.RateLimit.Mode, nil
}

func buildTrafficRetryBudget(ctx context.Context, cfg *Config, pool *pgxpool.Pool, metrics *gateway.Metrics, log *slog.Logger) (*gateway.RetryBudget, error) {
	mode, err := trafficCounterMode(cfg)
	if err != nil {
		return nil, err
	}
	url, err := retryBudgetRedisURL(osGetenv)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-internal: %w", err)
	}
	if url != "" {
		b, err := gateway.NewRedisRetryBudget(ctx, url, 0)
		if err != nil {
			return nil, fmt.Errorf("gatewayd-internal: connect shared retry budget: %w", err)
		}
		metrics.SetRetryBudgetShared(true)
		metrics.SetRetryBudgetBackendID(b.BackendID())
		log.Info("gatewayd-internal: shared retry budget enabled", "backend", "redis")
		return b, nil
	}
	if mode == "local" {
		metrics.SetRetryBudgetShared(false)
		return gateway.NewRetryBudget(0, nil), nil
	}
	if pool == nil {
		return nil, errors.New("gatewayd-internal: shared traffic counters require a Postgres pool")
	}
	backend := state.NewPGTrafficRetryBackend(pool)
	b, err := gateway.NewSharedRetryBudget(backend)
	if err != nil {
		return nil, err
	}
	metrics.SetRetryBudgetShared(true)
	metrics.SetRetryBudgetBackendID(b.BackendID())
	log.Info("gatewayd-internal: shared retry budget enabled", "backend", "postgres")
	go pruneTrafficRetryCounters(ctx, backend, log)
	return b, nil
}

func pruneTrafficRetryCounters(ctx context.Context, backend *state.PGTrafficRetryBackend, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruneCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := backend.Prune(pruneCtx)
			cancel()
			if err != nil {
				log.Warn("prune shared retry counters", "err", err)
			}
		}
	}
}

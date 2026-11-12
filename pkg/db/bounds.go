package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/pgpoolbounds"
)

// ConfigureTimeouts applies the same connection, acquisition and fallback
// query bounds as daemon pools without changing roles, DSNs or pool sizes.
// Call before NewWithConfig. An existing tracer is retained.
func ConfigureTimeouts(cfg *pgxpool.Config) {
	pgpoolbounds.ConfigureTimeouts(cfg)
}

// NewBoundedPool opens a non-daemon pool with an explicit concurrency budget.
// Daemons use OpenWithAppName so the fleet capacity model remains authoritative.
func NewBoundedPool(ctx context.Context, dsn string, maxConnections int32) (*pgxpool.Pool, error) {
	if maxConnections < 1 {
		return nil, fmt.Errorf("db: pool connection budget must be positive")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConnections
	cfg.MinConns = min(cfg.MinConns, maxConnections)
	cfg.MinIdleConns = min(cfg.MinIdleConns, maxConnections)
	ConfigureTimeouts(cfg)
	return pgxpool.NewWithConfig(ctx, cfg)
}

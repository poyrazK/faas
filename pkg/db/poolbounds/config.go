// Package poolbounds applies pgx deadlines without daemon wiring or migrations.
package poolbounds

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ConnectTimeout   = 5 * time.Second
	AcquireTimeout   = 5 * time.Second
	StatementTimeout = 5 * time.Minute
)

// ConfigureTimeouts bounds connection setup, acquisition and statements without
// caller deadlines. Pool sizes, explicit connection timeouts and tracers remain
// unchanged. Call before pgxpool.NewWithConfig.
func ConfigureTimeouts(cfg *pgxpool.Config) {
	if cfg.ConnConfig.ConnectTimeout <= 0 {
		cfg.ConnConfig.ConnectTimeout = ConnectTimeout
	}
	if cfg.ConnConfig.Tracer == nil {
		cfg.ConnConfig.Tracer = &Tracer{}
	}
}

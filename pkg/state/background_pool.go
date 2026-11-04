package state

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// backgroundWorkKey marks a context as a daemon's own background work.
type backgroundWorkKey struct{}

// WithBackgroundWork marks ctx as background work: periodic loops, reconcilers
// and sweeps rather than a customer request. A PgStore built with
// WithBackgroundPool runs its queries and transactions on that pool, so the
// loops cannot take every connection a request needs. apid's loops alone made
// about 29 pool acquires a second on an idle production control plane and
// found its 12-connection pool empty about 4,500 times an hour; customer
// requests queued behind them, and under load auth lookups timed out
// (Hunt 31 H31-8, H31-11).
func WithBackgroundWork(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundWorkKey{}, struct{}{})
}

// IsBackgroundWork reports whether ctx was marked by WithBackgroundWork.
func IsBackgroundWork(ctx context.Context) bool {
	return ctx.Value(backgroundWorkKey{}) != nil
}

// WithBackgroundPool gives background work its own pool. Session-scoped
// connections (LISTEN, advisory locks through the direct pool) stay on the
// primary pool.
func WithBackgroundPool(pool *pgxpool.Pool) StoreOption {
	return func(options *storeOptions) { options.backgroundPool = pool }
}

func configuredBackgroundPool(options []StoreOption) *pgxpool.Pool {
	var config storeOptions
	for _, option := range options {
		option(&config)
	}
	return config.backgroundPool
}

// route picks the pool for ctx.
func (p *orgAccessPool) route(ctx context.Context) *pgxpool.Pool {
	if p.Background != nil && IsBackgroundWork(ctx) {
		return p.Background
	}
	return p.Pool
}

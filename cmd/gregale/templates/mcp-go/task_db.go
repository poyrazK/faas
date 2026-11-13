package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Workers each need a query slot. Reserve four more for LISTEN, polling,
// maintenance and foreground requests; additional callers have a bounded wait.
func configureTaskDatabasePool(cfg *pgxpool.Config, workers int) {
	cfg.MaxConns = int32(workers + 4)
	cfg.MinConns = min(cfg.MinConns, cfg.MaxConns)
	cfg.MinIdleConns = min(cfg.MinIdleConns, cfg.MaxConns)
	if cfg.ConnConfig.ConnectTimeout <= 0 {
		cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	}
	cfg.ConnConfig.Tracer = taskDatabaseTracer{}
}

type taskDatabaseTracer struct{}
type taskAcquireCancelKey struct{}
type taskQueryCancelKey struct{}

func (taskDatabaseTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	return context.WithValue(ctx, taskAcquireCancelKey{}, cancel)
}

func (taskDatabaseTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	ctx.Value(taskAcquireCancelKey{}).(context.CancelFunc)()
}

func (taskDatabaseTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	cancel := context.CancelFunc(func() {})
	if _, bounded := ctx.Deadline(); !bounded {
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
	}
	return context.WithValue(ctx, taskQueryCancelKey{}, cancel)
}

func (taskDatabaseTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	ctx.Value(taskQueryCancelKey{}).(context.CancelFunc)()
}

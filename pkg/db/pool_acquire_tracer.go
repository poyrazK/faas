// pool_acquire_tracer.go — counts pool acquisitions a caller abandoned while
// waiting, which is what <daemon>_db_pool_canceled_acquires_total and the
// FaasDBPoolStarved page are specified to measure (spec §12).
//
// pgxpool's own Stat().CanceledAcquireCount() is broader: puddle counts an
// acquisition whose context had already ended before Acquire was called,
// with idle connections available and nothing waited for. A client that
// disconnects just before its handler queries is enough. Production-us
// paged FaasDBPoolStarved for schedd on fsn-3 with 9-10 idle connections,
// no empty acquires and no acquire wait time (hunt #10, H10-108).
package db

import (
	"context"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// acquireAbandonTracer implements pgxpool.AcquireTracer. ConnConfig.Tracer is
// typed as pgx.QueryTracer. Its query hooks also bound calls without a caller
// deadline, including when a transaction pooler cannot accept session settings.
type acquireAbandonTracer struct {
	abandoned atomic.Int64
}

// precanceledAcquireKey marks an acquisition whose context had ended before
// it began. Only that rare path allocates.
type precanceledAcquireKey struct{}
type acquireCancelKey struct{}
type queryCancelKey struct{}

// instrumentAcquires installs the tracer on an ordinary daemon pool. A config
// that already carries a tracer keeps it; the collector then falls back to
// pgxpool's own count.
func instrumentAcquires(cfg *pgxpool.Config) {
	if cfg.ConnConfig.Tracer == nil {
		cfg.ConnConfig.Tracer = &acquireAbandonTracer{}
	}
}

// abandonedAcquires reports the tracer's count for pool, or false when the
// pool was built without it.
func abandonedAcquires(pool *pgxpool.Pool) (int64, bool) {
	t, ok := pool.Config().ConnConfig.Tracer.(*acquireAbandonTracer)
	if !ok {
		return 0, false
	}
	return t.abandoned.Load(), true
}

func (t *acquireAbandonTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	if ctx.Err() != nil {
		return context.WithValue(ctx, precanceledAcquireKey{}, struct{}{})
	}
	ctx, cancel := context.WithTimeout(ctx, AcquireTimeout)
	return context.WithValue(ctx, acquireCancelKey{}, cancel)
}

// TraceAcquireEnd counts a failed acquisition only when the caller's context
// ended during it. A dial or authentication failure under a live context is
// not an abandonment.
func (t *acquireAbandonTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	if cancel, ok := ctx.Value(acquireCancelKey{}).(context.CancelFunc); ok {
		defer cancel()
	}
	if data.Err == nil || ctx.Err() == nil || ctx.Value(precanceledAcquireKey{}) != nil {
		return
	}
	t.abandoned.Add(1)
}

func (t *acquireAbandonTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return boundQuery(ctx)
}

func (t *acquireAbandonTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	finishQuery(ctx)
}

// Explicit caller deadlines retain their semantics (for example migrations).
// A private cancel key is always installed, so nested tracer calls cannot
// cancel an enclosing query's context.
func boundQuery(ctx context.Context) context.Context {
	cancel := context.CancelFunc(func() {})
	if _, bounded := ctx.Deadline(); !bounded {
		ctx, cancel = context.WithTimeout(ctx, DaemonStatementTimeout)
	}
	return context.WithValue(ctx, queryCancelKey{}, cancel)
}

func finishQuery(ctx context.Context) {
	if cancel, ok := ctx.Value(queryCancelKey{}).(context.CancelFunc); ok {
		cancel()
	}
}

func (t *acquireAbandonTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return boundQuery(ctx)
}
func (t *acquireAbandonTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}
func (t *acquireAbandonTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchEndData) {
	finishQuery(ctx)
}
func (t *acquireAbandonTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	return boundQuery(ctx)
}
func (t *acquireAbandonTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromEndData) {
	finishQuery(ctx)
}

func (t *acquireAbandonTracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, _ pgx.TracePrepareStartData) context.Context {
	return boundQuery(ctx)
}
func (t *acquireAbandonTracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TracePrepareEndData) {
	finishQuery(ctx)
}

package pgpoolbounds

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tracer implements pgxpool.AcquireTracer. ConnConfig.Tracer is
// typed as pgx.QueryTracer. Its query hooks also bound calls without a caller
// deadline, including when a transaction pooler cannot accept session settings.
type Tracer struct {
	abandoned        atomic.Int64
	permissionDenied atomic.Int64
}

// insufficientPrivilege is SQLSTATE 42501, what Postgres returns when the
// connected role lacks a privilege on the object a statement touches.
const insufficientPrivilege = "42501"

// precanceledAcquireKey marks an acquisition whose context had ended before
// it began. Only that rare path allocates.
type precanceledAcquireKey struct{}
type acquireCancelKey struct{}
type queryCancelKey struct{}

// AbandonedAcquires counts callers whose context ended while waiting, excluding
// contexts that had already ended before acquisition began.
func (t *Tracer) AbandonedAcquires() int64 { return t.abandoned.Load() }

// PermissionDenied counts statements Postgres refused with SQLSTATE 42501.
// With each daemon on its own role, a non-zero rate means the role's grants
// miss something its code does (ADR-20261023104734 step 3).
func (t *Tracer) PermissionDenied() int64 { return t.permissionDenied.Load() }

func (t *Tracer) observe(err error) {
	var pgErr *pgconn.PgError
	if err != nil && errors.As(err, &pgErr) && pgErr.Code == insufficientPrivilege {
		t.permissionDenied.Add(1)
	}
}

func (t *Tracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	if ctx.Err() != nil {
		return context.WithValue(ctx, precanceledAcquireKey{}, struct{}{})
	}
	ctx, cancel := context.WithTimeout(ctx, AcquireTimeout)
	return context.WithValue(ctx, acquireCancelKey{}, cancel)
}

// TraceAcquireEnd counts a failed acquisition only when the caller's context
// ended during it. A dial or authentication failure under a live context is
// not an abandonment.
func (t *Tracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	if cancel, ok := ctx.Value(acquireCancelKey{}).(context.CancelFunc); ok {
		defer cancel()
	}
	if data.Err == nil || ctx.Err() == nil || ctx.Value(precanceledAcquireKey{}) != nil {
		return
	}
	t.abandoned.Add(1)
}

func (t *Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return boundQuery(ctx)
}

func (t *Tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	t.observe(data.Err)
	finishQuery(ctx)
}

// Explicit caller deadlines retain their semantics (for example migrations).
// A private cancel key is always installed, so nested tracer calls cannot
// cancel an enclosing query's context.
func boundQuery(ctx context.Context) context.Context {
	cancel := context.CancelFunc(func() {})
	if _, bounded := ctx.Deadline(); !bounded {
		ctx, cancel = context.WithTimeout(ctx, StatementTimeout)
	}
	return context.WithValue(ctx, queryCancelKey{}, cancel)
}

func finishQuery(ctx context.Context) {
	if cancel, ok := ctx.Value(queryCancelKey{}).(context.CancelFunc); ok {
		cancel()
	}
}

func (t *Tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return boundQuery(ctx)
}

// TraceBatchQuery counts each refused statement of a batch; TraceBatchEnd's
// error repeats the first of them, so it is not counted again.
func (t *Tracer) TraceBatchQuery(_ context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	t.observe(data.Err)
}
func (t *Tracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchEndData) {
	finishQuery(ctx)
}
func (t *Tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	return boundQuery(ctx)
}
func (t *Tracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	t.observe(data.Err)
	finishQuery(ctx)
}

func (t *Tracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, _ pgx.TracePrepareStartData) context.Context {
	return boundQuery(ctx)
}
func (t *Tracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	t.observe(data.Err)
	finishQuery(ctx)
}

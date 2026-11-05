package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcquireDeadlineAndTimerLifetime(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	tracer := &acquireAbandonTracer{}
	ctx := tracer.TraceAcquireStart(parent, nil, pgxpool.TraceAcquireStartData{})
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > AcquireTimeout {
		t.Fatal("pool wait did not get its fallback deadline")
	}
	tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{})
	if ctx.Err() == nil || parent.Err() != nil || tracer.abandoned.Load() != 0 {
		t.Fatal("acquire completion leaked its timer, canceled the parent, or counted a successful acquire as abandoned")
	}
}

func TestQueryDeadlineCoversRowsUntilCompletion(t *testing.T) {
	tracer := &acquireAbandonTracer{}
	unbounded := context.Background()
	ctx := tracer.TraceQueryStart(unbounded, nil, pgx.TraceQueryStartData{})
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("query without a caller deadline remained unbounded")
	}
	if ctx.Err() != nil {
		t.Fatal("query context ended before rows were consumed")
	}
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if ctx.Err() == nil || unbounded.Err() != nil {
		t.Fatal("query completion must release only its own timer")
	}
	parent, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	bounded := tracer.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{})
	deadline, _ := bounded.Deadline()
	want, _ := parent.Deadline()
	tracer.TraceQueryEnd(bounded, nil, pgx.TraceQueryEndData{})
	if deadline != want || parent.Err() != nil {
		t.Fatal("an explicit query deadline was changed or canceled")
	}
}

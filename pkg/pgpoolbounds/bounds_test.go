package pgpoolbounds

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type configuredTracer struct{}

func (*configuredTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (*configuredTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAcquireDeadlineConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		connect time.Duration
		tracer  pgx.QueryTracer
	}{
		{name: "defaults"},
		{name: "explicit connect timeout", connect: 2 * time.Second},
		{name: "existing tracer", tracer: &configuredTracer{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := pgxpool.ParseConfig("postgres://faas@localhost/faas")
			if err != nil {
				t.Fatal(err)
			}
			cfg.ConnConfig.ConnectTimeout = tc.connect
			cfg.ConnConfig.Tracer = tc.tracer
			cfg.MaxConns, cfg.MinConns, cfg.MinIdleConns = 7, 2, 1
			ConfigureTimeouts(cfg)
			want := tc.connect
			if want == 0 {
				want = ConnectTimeout
			}
			if cfg.ConnConfig.ConnectTimeout != want || cfg.MaxConns != 7 || cfg.MinConns != 2 || cfg.MinIdleConns != 1 {
				t.Fatal("configuration changed an explicit timeout or pool budget")
			}
			if tc.tracer != nil && cfg.ConnConfig.Tracer != tc.tracer {
				t.Fatal("configuration replaced the caller's tracer")
			}
			if tc.tracer == nil {
				if _, ok := cfg.ConnConfig.Tracer.(*Tracer); !ok {
					t.Fatal("unconfigured pool did not receive the deadline tracer")
				}
			}
		})
	}
}

func TestAcquireDeadlineAbandonmentCounts(t *testing.T) {
	for _, kind := range []string{"precanceled", "abandoned", "dial-error"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "precanceled" {
				cancel()
			}
			tracer := &Tracer{}
			ctx := tracer.TraceAcquireStart(parent, nil, pgxpool.TraceAcquireStartData{})
			if kind == "abandoned" {
				cancel()
			}
			tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{Err: errors.New("acquire failed")})
			var want int64
			if kind == "abandoned" {
				want = 1
			}
			if got := tracer.AbandonedAcquires(); got != want {
				t.Fatalf("abandoned acquires = %d, want %d", got, want)
			}
		})
	}
}

func TestAcquireDeadlineAndTimerLifetime(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	tracer := &Tracer{}
	ctx := tracer.TraceAcquireStart(parent, nil, pgxpool.TraceAcquireStartData{})
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > AcquireTimeout {
		t.Fatal("pool wait did not get its fallback deadline")
	}
	tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{})
	if ctx.Err() == nil || parent.Err() != nil || tracer.AbandonedAcquires() != 0 {
		t.Fatal("acquire completion leaked its timer, canceled the parent, or counted a successful acquire as abandoned")
	}
}

func TestQueryDeadlineCoversRowsUntilCompletion(t *testing.T) {
	tracer := &Tracer{}
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

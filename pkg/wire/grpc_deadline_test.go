package wire

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
)

func TestDeadlineUnaryInterceptor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		def          time.Duration
		callerHas    time.Duration // 0 = no caller deadline
		wantDeadline bool
		wantWithin   time.Duration // upper bound on the observed deadline from now
		wantCounted  float64
	}{
		{name: "no caller deadline gets default", def: 60 * time.Second, wantDeadline: true, wantWithin: 60 * time.Second, wantCounted: 1},
		{name: "caller deadline preserved", def: 60 * time.Second, callerHas: 5 * time.Second, wantDeadline: true, wantWithin: 5 * time.Second, wantCounted: 0},
		{name: "disabled still counts but does not bound", def: 0, wantDeadline: false, wantCounted: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ops := NewOpsMetrics("test")
			ic := deadlineUnaryInterceptor(tc.def, func() *OpsMetrics { return ops }, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx := context.Background()
			if tc.callerHas > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.callerHas)
				defer cancel()
			}
			var seen context.Context
			invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				seen = ctx
				return nil
			}
			before := time.Now()
			if err := ic(ctx, "/faas.Schedd/Wake", nil, nil, nil, invoker); err != nil {
				t.Fatalf("interceptor: %v", err)
			}
			dl, ok := seen.Deadline()
			if ok != tc.wantDeadline {
				t.Fatalf("deadline present=%v want %v", ok, tc.wantDeadline)
			}
			if ok && dl.Sub(before) > tc.wantWithin+time.Second {
				t.Fatalf("deadline %s from call exceeds %s", dl.Sub(before), tc.wantWithin)
			}
			got := testutil.ToFloat64(ops.grpcClientCallsWithoutDeadline.WithLabelValues("/faas.Schedd/Wake"))
			if got != tc.wantCounted {
				t.Fatalf("counter=%v want %v", got, tc.wantCounted)
			}
		})
	}
}

func TestDeadlineUnaryInterceptorWarnsOncePerMethod(t *testing.T) {
	t.Parallel()
	var records int
	h := &countingHandler{onRecord: func() { records++ }}
	ic := deadlineUnaryInterceptor(time.Second, func() *OpsMetrics { return nil }, slog.New(h))
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error { return nil }
	for range 3 {
		_ = ic(context.Background(), "/faas.Vmmd/PauseAndSnapshot", nil, nil, nil, invoker)
	}
	_ = ic(context.Background(), "/faas.Vmmd/Destroy", nil, nil, nil, invoker)
	if records != 2 {
		t.Fatalf("warn records=%d want 2 (one per distinct method)", records)
	}
}

func TestDefaultGRPCDeadlineEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":    defaultGRPCDeadline,
		"0":   0,
		"15s": 15 * time.Second,
		"bad": defaultGRPCDeadline,
		"-1s": defaultGRPCDeadline,
	}
	for raw, want := range cases {
		t.Setenv(DefaultGRPCDeadlineEnv, raw)
		if got := DefaultGRPCDeadline(); got != want {
			t.Fatalf("%q: got %s want %s", raw, got, want)
		}
	}
}

// countingHandler is a minimal slog.Handler that counts emitted records.
type countingHandler struct{ onRecord func() }

func (h *countingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h *countingHandler) Handle(context.Context, slog.Record) error { h.onRecord(); return nil }
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h *countingHandler) WithGroup(string) slog.Handler             { return h }

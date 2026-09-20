package wire

// gRPC client default-deadline interceptor (ADR-190, decision 1).
//
// On 2026-09-03 a PauseAndSnapshot RPC with no deadline blocked in
// grpc waitOnHeader for 10+ minutes. It ran on schedd's single
// notify goroutine, so the reaper, cron, and watchdog ticks all
// stopped while the daemon kept answering /metrics and looked
// healthy (pkg/sched/engine.go SnapshotTimeout godoc records the
// incident). Per-call deadlines are the documented contract at every
// engine call site, but a contract without enforcement decays: that
// call had none.
//
// This interceptor is the enforcement. Every unary RPC issued through
// wire.DialContext gets a deadline. A caller that already set one is
// never shortened; a caller that forgot gets DefaultGRPCDeadline and
// is counted on grpc_client_calls_without_deadline_total{method} so
// the remaining sites are visible on /metrics rather than in a
// post-mortem.
//
// Streams are deliberately not covered: bridge sessions are 24 h by
// design (pkg/vmmdgrpc/forward.go streamBridgeSessionDeadline) and
// the stream RPCs own their lifetimes.

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
)

// DefaultGRPCDeadlineEnv names the operator override for the default
// unary deadline. A Go duration; "0" disables the interceptor (calls
// without a deadline pass through unchanged, still counted). Unset or
// unparsable falls back to defaultGRPCDeadline.
const DefaultGRPCDeadlineEnv = "FAAS_GRPC_DEFAULT_DEADLINE"

// defaultGRPCDeadline is the ceiling applied to a unary RPC whose
// caller set no deadline. 60 s sits above every engine-side budget
// (ColdBootTimeout 35 s, SnapshotTimeout 25 s, the scaled snapshot
// budget for 1 GiB instances) so a correctly budgeted call never sees
// it, while a forgotten one is bounded to one minute instead of
// forever.
const defaultGRPCDeadline = 60 * time.Second

// DefaultGRPCDeadline reads DefaultGRPCDeadlineEnv. See the constant
// for the fallback and the "0" disable semantics.
func DefaultGRPCDeadline() time.Duration {
	raw := os.Getenv(DefaultGRPCDeadlineEnv)
	if raw == "" {
		return defaultGRPCDeadline
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return defaultGRPCDeadline
	}
	return d
}

// DeadlineUnaryInterceptor returns the client interceptor that applies
// def to any unary call whose context carries no deadline. def <= 0
// disables the deadline but keeps the counter so operators can still
// find unbudgeted call sites.
func DeadlineUnaryInterceptor(def time.Duration) grpc.UnaryClientInterceptor {
	return deadlineUnaryInterceptor(def, func() *OpsMetrics { return defaultOps }, slog.Default())
}

// deadlineUnaryInterceptor is the testable core. ops is resolved per
// call because defaultOps is registered after wire.DialContext may
// already have been called during daemon boot.
func deadlineUnaryInterceptor(def time.Duration, ops func() *OpsMetrics, log *slog.Logger) grpc.UnaryClientInterceptor {
	var warned sync.Map
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		ops().ObserveGRPCCallWithoutDeadline(method)
		if _, seen := warned.LoadOrStore(method, struct{}{}); !seen && log != nil {
			log.Warn("wire: unary gRPC call without deadline",
				"method", method, "applied_default", def.String())
		}
		if def <= 0 {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		ctx, cancel := context.WithTimeout(ctx, def)
		defer cancel()
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// ObserveGRPCCallWithoutDeadline increments
// <daemon>_grpc_client_calls_without_deadline_total{method}. nil-safe
// so the interceptor works before RegisterDefaultOps and in tests.
func (m *OpsMetrics) ObserveGRPCCallWithoutDeadline(method string) {
	if m == nil || m.grpcClientCallsWithoutDeadline == nil {
		return
	}
	m.grpcClientCallsWithoutDeadline.WithLabelValues(method).Inc()
}

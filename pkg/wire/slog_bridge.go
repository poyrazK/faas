// Slog bridge — pkg/wire/slog_bridge.go (issue #555 PR-3).
//
// Wraps an inner slog.Handler so that, on every record, the OTel
// SpanContext on the record's context is lifted onto the record as
// trace_id / span_id attributes (FieldTraceID, FieldSpanID). Wire it
// as the inner handler in daemon.go::Logger() so every platform-wide
// log line carries the trace IDs — issue #555 acceptance #2 ("grep
// '"trace_id":"abc..."' /var/log/faas/*.jsonl" returns one span per
// daemon").
//
// The bridge is the only place in pkg/wire that imports OTel
// (oteltrace.SpanContextFromContext). The import is restricted to
// the bridge so a future OTel upgrade only touches this file.
package wire

import (
	"context"
	"log/slog"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// traceLoggingHandler wraps an inner slog.Handler and lifts OTel span
// context from each record's context onto the emitted record. The
// inner handler is the standard slog.NewJSONHandler over os.Stderr.
//
// Why a wrapper, not a ReplaceAttr on the inner handler: the OTel
// SpanContext lives on the record's context, and slog's ReplaceAttr
// does not see the context. A Handler wrap is the idiomatic way to
// read the context per record.
//
// The wrapper is safe for concurrent use — the standard slog
// contract is concurrency-safe for a Handler (per pkg.go.dev).
type traceLoggingHandler struct {
	inner slog.Handler
}

// NewSlogBridge wraps inner with the OTel-aware handler. Returns
// inner unchanged when inner is nil (defensive — keeps callers from
// having to nil-check on every code path).
func NewSlogBridge(inner slog.Handler) slog.Handler {
	if inner == nil {
		return slog.Default().Handler()
	}
	return &traceLoggingHandler{inner: inner}
}

// Enabled delegates to the inner handler — the bridge never filters
// on its own. A log line that the inner handler would drop is also
// dropped here, regardless of whether OTel span context exists.
func (h *traceLoggingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle builds the record, lifts trace_id/span_id from ctx, then
// delegates to the inner handler. The lift is best-effort: when no
// span is active, the record is emitted unchanged.
//
// The record's existing PCtx (programmer-context, slog's per-record
// context) is the source — slog passes it through to Handle so we
// can read it. Adding attributes via record.AddAttrs is the standard
// way to inject OTel span context without duplicating the rest of
// the record.
func (h *traceLoggingHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := oteltrace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		r.AddAttrs(
			slog.String(FieldTraceID, sc.TraceID().String()),
			slog.String(FieldSpanID, sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a child handler that stamps the attrs on every
// record the way the inner handler would. The OTel lift still runs
// on every Handle call (the wrapper stays outside the WithAttrs
// delegation so child handlers don't have to re-implement the lift).
func (h *traceLoggingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceLoggingHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup mirrors WithAttrs — the OTel lift stays outside the
// inner handler's group semantics. A group is a record-attr
// concern, not a context concern.
func (h *traceLoggingHandler) WithGroup(name string) slog.Handler {
	return &traceLoggingHandler{inner: h.inner.WithGroup(name)}
}

// Package wire — logging.go holds the cross-daemon standard slog envelope
// (issue #517). NewCorrelationLogger attaches the canonical correlation
// fields to every record a child logger emits, so a single inbound request
// produces logs across gatewayd → schedd → vmmd that all carry the same
// request_id and (on a cold wake) the same wake_id.
//
// The envelope is intentionally a thin wrapper around slog.Logger.With so
// existing log call sites (slog.Info / slog.Warn / slog.Error / slog.Debug)
// need no API change — the producer passes the correlation logger down at
// construction time and the records carry the fields automatically.
//
// Empty fields are dropped (not emitted as empty attributes) so a producer
// can pass a half-filled struct without polluting downstream log filters.
// Each field maps to a separate slog attribute key, mirroring the wire
// header convention (lowercase x-faas-…) but dropping the x-faas- prefix
// on the log side; the prefix is a wire artefact, not a log convention.
//
// Why this lives in pkg/wire and not pkg/middleware: pkg/wire is the
// shared transport/observability home (NewOpsMetrics, GRPC dial helpers,
// peer auth helpers). The correlation helper is a transport-layer
// construct — it crosses HTTP, gRPC, and slog boundaries — so pkg/wire is
// the right layer. pkg/middleware stays focused on the inbound HTTP path.
package wire

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Correlation field names emitted as slog attributes. Stable contract:
// downstream log filters (Loki, the §12 dashboard alerts) match on these
// exact keys. Renaming any of these is a breaking change.
// FieldTraceID and FieldSpanID are the OTel span-context fields an
// upstream ingress (gatewayd-public) or a wired wire.OtelInit (PR-1
// of issue #555) lifts onto every slog record. They sit alongside the
// existing correlation envelope: wake_id is the OTel trace_id
// (UUIDv7, see schedd engine Phase 2), span_id is the currently
// active span. Renaming any of these is a breaking change for Loki
// filters and the §12 dashboard.
const (
	FieldRequestID    = "request_id"
	FieldWakeID       = "wake_id"
	FieldAppID        = "app_id"
	FieldDeploymentID = "deployment_id"
	FieldInstanceID   = "instance_id"
	FieldInvocationID = "invocation_id"
	FieldTraceID      = "trace_id"
	FieldSpanID       = "span_id"
	FieldDaemon       = "daemon"
)

// CorrelationFields is the canonical set of fields that identify a single
// inbound request or a single wake lifecycle. The struct is additive —
// new fields (e.g. cron_id, build_id, trace_id, span_id) can be added
// without breaking the log contract as long as the wire-side metadata
// helper in grpcmetadata.go carries them too.
//
// All fields are optional; a producer may pass a half-filled struct and
// the empty fields are silently dropped from the emitted slog record.
//
// TraceID and SpanID are the OTel span context (issue #555). TraceID is
// canonically the same value as WakeID on a cold-wake path (the engine
// mints a UUIDv7 at Phase 2 and we reuse it as the OTel trace_id per
// the design decision on issue #555). On a warm hit where no wake_id
// is minted, the gateway-level traceparent is the source. The two
// fields are kept separately so the slog envelope stays clean when
// producers carry one but not the other.
type CorrelationFields struct {
	RequestID    string
	WakeID       string
	AppID        string
	DeploymentID string
	InstanceID   string
	InvocationID string
	TraceID      string
	SpanID       string
}

// FromContext returns the correlation fields stored on ctx by the inbound
// gRPC metadata reader (CorrelationFromIncoming in grpcmetadata.go). The
// boolean is false when no fields were set; the caller can then either
// mint a fresh request_id (gatewayd) or pass through (intermediate hops).
//
// Reads via the same context key the metadata helper writes; both helpers
// live in pkg/wire so the read/write pair stays in lockstep.
func FromContext(ctx context.Context) (CorrelationFields, bool) {
	if ctx == nil {
		return CorrelationFields{}, false
	}
	v, ok := ctx.Value(correlationKey{}).(CorrelationFields)
	if !ok || v == (CorrelationFields{}) {
		return CorrelationFields{}, false
	}
	return v, true
}

// WithContext stores fields on ctx. Empty fields are preserved (the helper
// is mechanical) so a downstream reader can still distinguish "I asked for
// an empty wake_id" from "I never set one". The boolean zero-value
// comparison in FromContext filters the latter.
//
// Mirrors the pkg/middleware/requestid.go WithRequestID / RequestIDFrom
// pair so the read/write shape stays symmetric across the codebase.
func WithContext(ctx context.Context, fields CorrelationFields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, correlationKey{}, fields)
}

// NewCorrelationLogger returns a child slog.Logger that stamps fields onto
// every record. base is the underlying handler (typically slog.NewJSONHandler
// over os.Stderr in production, or a discard handler in tests); fields is
// the correlation set. The returned logger is safe for concurrent use —
// the standard library's slog.Logger.With is concurrency-safe per its
// documented contract.
//
// Usage pattern:
//
//	logger := wire.NewCorrelationLogger(
//	    slog.New(slog.NewJSONHandler(os.Stderr, nil)),
//	    wire.CorrelationFields{RequestID: middleware.NewRequestID()},
//	)
//	logger.Info("hello") // emits {"request_id": "...", "msg": "hello"}
//
// An additional "daemon" attribute is always emitted so the operator can
// filter the aggregate log stream by source daemon without consulting a
// separate index. daemon is the literal value the caller passes; the
// cmd/<daemon>/main.go constructors pass "gatewayd", "schedd", "vmmd",
// "apid", "builderd", "imaged".
func NewCorrelationLogger(base *slog.Logger, fields CorrelationFields, daemon string) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	attrs := make([]any, 0, 14)
	// daemon is an in-tree constant ("schedd", "vmmd", …), never
	// attacker-influenced — sanitization is unnecessary.
	if daemon != "" {
		attrs = append(attrs, FieldDaemon, daemon)
	}
	attrs = appendCorrelationAttrs(attrs, fields)
	return base.With(attrs...)
}

// WithCorrelationFields returns a derived child logger that adds (or
// overrides) the named correlation fields on top of the base logger's
// envelope. Useful when a handler knows its app_id / instance_id at log
// time but the daemon-wide logger doesn't. The shape mirrors
// slog.Logger.With so call sites feel idiomatic:
//
//	logger := wire.WithCorrelationFields(e.log, wire.CorrelationFields{AppID: app.ID})
//	logger.Info("wake admit", "wake_id", wakeID)
//
// Does NOT stamp "daemon" / "version" — those are wire.Daemon's envelope.
// Callers that wrap slog.Default() (no daemon envelope) should call
// NewCorrelationLogger instead to keep the daemon name on every record.
//
// Cannot be a method on *slog.Logger because that type lives in the
// standard library; Go forbids defining new methods on non-local types.
// The free-function shape is the idiomatic alternative.
func WithCorrelationFields(base *slog.Logger, fields CorrelationFields) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	attrs := appendCorrelationAttrs(nil, fields)
	return base.With(attrs...)
}

// appendCorrelationAttrs is the shared emit half of the two envelope
// helpers. It routes every correlation value through logsanitize.Field
// — correlation IDs cross protocol boundaries (HTTP headers, gRPC MD,
// proto fields) and any of those can carry attacker-controlled bytes
// (per CLAUDE.md §11 ship-blocker and the issue #517 PR-D sanitizer
// audit). Sanitization at the canonical lift point means every
// downstream record inherits the protection without per-call-site
// effort. The daemon name is sanitized separately by NewCorrelationLogger.
func appendCorrelationAttrs(attrs []any, fields CorrelationFields) []any {
	if fields.RequestID != "" {
		attrs = append(attrs, FieldRequestID, logsanitize.Field(fields.RequestID))
	}
	if fields.WakeID != "" {
		attrs = append(attrs, FieldWakeID, logsanitize.Field(fields.WakeID))
	}
	if fields.AppID != "" {
		attrs = append(attrs, FieldAppID, logsanitize.Field(fields.AppID))
	}
	if fields.DeploymentID != "" {
		attrs = append(attrs, FieldDeploymentID, logsanitize.Field(fields.DeploymentID))
	}
	if fields.InstanceID != "" {
		attrs = append(attrs, FieldInstanceID, logsanitize.Field(fields.InstanceID))
	}
	if fields.InvocationID != "" {
		attrs = append(attrs, FieldInvocationID, logsanitize.Field(fields.InvocationID))
	}
	if fields.TraceID != "" {
		attrs = append(attrs, FieldTraceID, logsanitize.Field(fields.TraceID))
	}
	if fields.SpanID != "" {
		attrs = append(attrs, FieldSpanID, logsanitize.Field(fields.SpanID))
	}
	return attrs
}

// correlationKey is the unexported context key type. Using an empty struct
// avoids collisions with keys defined by other packages (Go's net/http
// package convention; net/http uses struct{} keys for its own values).
type correlationKey struct{}

// NewRequestID returns a 128-bit random hex string (uuid-like, no dashes).
// Crypto/rand so we don't pull google/uuid just for this; on the
// extremely-unlikely rand.Read failure it emits zero rather than panicking
// in the request hot path.
//
// Lives in pkg/wire (rather than pkg/middleware) because every daemon's
// wire.Daemon bootstrap mints one at startup — wire is the layered-in-below
// package. pkg/middleware.NewRequestID is preserved as a thin re-export so
// existing callers don't change.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

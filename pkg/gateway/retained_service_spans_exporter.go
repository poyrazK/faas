package gateway

// Retained service-span ingestion bridges Gregale-owned service-proxy spans
// into the same bounded accumulator used by customer OTLP ingestion. It does
// not retain arbitrary daemon or customer spans: only an authorized
// managed_binding/service_proxy span carrying the account identity stamped by
// ServiceProxy is eligible.

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const retainedSpanAccountIDAttribute = "gregale.internal.account_id"

// RetainedServiceSpansExporter copies authorized platform-owned service spans
// into a SpansAccumulator. ExportSpans only performs bounded in-memory work and
// is therefore safe to mount through sdktrace.WithSyncer.
type RetainedServiceSpansExporter struct {
	acc *SpansAccumulator
	log *slog.Logger
}

// NewRetainedServiceSpansExporter returns a platform span exporter backed by
// acc. A nil accumulator is a programming error because silently accepting it
// would make the debugger appear enabled while dropping every span.
func NewRetainedServiceSpansExporter(acc *SpansAccumulator, log *slog.Logger) *RetainedServiceSpansExporter {
	if acc == nil {
		panic("RetainedServiceSpansExporter: nil accumulator")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RetainedServiceSpansExporter{acc: acc, log: log}
}

// ExportSpans filters and converts completed service-proxy spans. The
// account-routing attribute is removed from the customer-visible summary; it
// exists only to bind the trusted in-process span to apid's tenant-scoped
// writer RPC.
func (e *RetainedServiceSpansExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	var exportErrors []error
	for _, span := range spans {
		attrs := attrsToMap(span.Attributes())
		if attrs["gregale.dependency.type"] != "managed_binding" || attrs["gregale.dependency.kind"] != "service_proxy" {
			continue
		}

		accountID, err := uuid.Parse(strings.TrimSpace(attrs[retainedSpanAccountIDAttribute]))
		if err != nil {
			e.log.Warn("retained service span dropped: account identity is invalid", "span", span.Name())
			continue
		}
		delete(attrs, retainedSpanAccountIDAttribute)

		spanContext := span.SpanContext()
		if !spanContext.IsValid() {
			continue
		}
		parentSpanID := ""
		if parent := span.Parent(); parent.IsValid() {
			parentSpanID = parent.SpanID().String()
		}
		start, end := span.StartTime(), span.EndTime()
		var duration uint64
		if end.After(start) {
			duration = uint64(end.Sub(start))
		}
		status, statusMessage := "ok", ""
		if span.Status().Code == codes.Error {
			status = "error"
			statusMessage = span.Status().Description
		}
		summary := summarizedSpan{
			TraceID:           spanContext.TraceID().String(),
			SpanID:            spanContext.SpanID().String(),
			ParentSpanID:      parentSpanID,
			Name:              span.Name(),
			Kind:              span.SpanKind().String(),
			StartTimeUnixNano: uint64(start.UnixNano()),
			EndTimeUnixNano:   uint64(end.UnixNano()),
			DurationNanos:     duration,
			Status:            status,
			StatusMessage:     statusMessage,
			Attributes:        attrs,
			DBStatement:       extractDBStatement(attrs),
		}
		if _, err := e.acc.Add(summary.TraceID, accountID, []summarizedSpan{summary}); err != nil {
			exportErrors = append(exportErrors, err)
		}
	}
	return errors.Join(exportErrors...)
}

// Shutdown is a no-op. The accumulator is drained separately after the tracer
// provider has stopped delivering spans.
func (*RetainedServiceSpansExporter) Shutdown(context.Context) error { return nil }

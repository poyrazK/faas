// Package gateway — trace_exporter.go (issue #555 PR-2). The
// TraceRingExporter is a sdktrace.SpanExporter that copies completed
// spans into the local TraceRing. The exporter is mounted alongside
// the OTLP/HTTP exporter (or by itself when no OTLP endpoint is set)
// so the in-memory ring is always populated, regardless of whether
// the operator has configured OTLP export.
//
// Why a custom exporter: the OTel SDK's WithBatcher takes one or
// more SpanExporter values; the BatchSpanProcessor fans out to
// all of them. By making the ring exporter a first-class exporter,
// the ring lifecycle is tied to the same BatchSpanProcessor the
// OTLP exporter uses — flush-on-shutdown, batch timeout, etc. are
// shared.
//
// Why not a span processor: SpanProcessor is what wraps the
// exporter at the SDK level, but the Processor API is "transform"
// (OnStart/OnEnd) which mutates the ReadOnlySpan. We want a
// sink, not a transform.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
)

// TraceRingExporter writes ReadOnlySpan values from the OTel SDK
// into a TraceRing. The exporter is concurrency-safe (the underlying
// ring is mutex-guarded).
type TraceRingExporter struct {
	ring *TraceRing
	log  *slog.Logger
	// now is the clock function for the LastSeen attribution. It
	// defaults to time.Now if nil.
	now func() time.Time
}

// NewTraceRingExporter returns an exporter that writes into ring.
// log is used for one-time boot diagnostics; nil disables logging.
func NewTraceRingExporter(ring *TraceRing, log *slog.Logger) *TraceRingExporter {
	if ring == nil {
		panic("TraceRingExporter: nil ring")
	}
	return &TraceRingExporter{ring: ring, log: log, now: time.Now}
}

// ExportSpans converts the SDK ReadOnlySpan values into the ring's
// Trace/SpanRow shape and writes them. The conversion is per-span
// (one Trace per unique trace_id) so a single ExportSpans call can
// populate many traces at once.
//
// The exporter is required to be safe under -race; the BatchSpanProcessor
// only invokes ExportSpans sequentially per processor, but the
// ring itself is mutex-guarded so future parallel exporters compose.
func (e *TraceRingExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	if e.log != nil {
		e.log.Debug("trace_exporter: ExportSpans", "count", len(spans))
	}
	// Group spans by trace_id so we Add each trace once.
	byTrace := make(map[string]*Trace, len(spans))
	for _, s := range spans {
		sc := s.SpanContext()
		if !sc.IsValid() {
			continue
		}
		traceID := sc.TraceID().String()
		t, ok := byTrace[traceID]
		if !ok {
			t = &Trace{TraceID: traceID}
			byTrace[traceID] = t
		}
		t.Spans = append(t.Spans, spanToRow(s))
	}
	now := e.now()
	for _, t := range byTrace {
		t.Started = earliestStart(t.Spans, now)
		t.LastSeen = now
		e.ring.Add(t)
	}
	return nil
}

// Shutdown is a no-op. The ring lives for the lifetime of the
// process; the BatchSpanProcessor.Shutdown call is what stops
// invoking ExportSpans.
func (e *TraceRingExporter) Shutdown(ctx context.Context) error {
	return nil
}

// spanToRow converts a ReadOnlySpan into the ring's SpanRow JSON
// shape. Attributes are flattened to strings (the OTel attribute
// value is any; the JSON shape is intentionally string-only because
// the operator's debug session is a grep not a query).
func spanToRow(s trace.ReadOnlySpan) SpanRow {
	sc := s.SpanContext()
	parent := s.Parent()
	row := SpanRow{
		TraceID:      sc.TraceID().String(),
		SpanID:       sc.SpanID().String(),
		ParentSpanID: parent.SpanID().String(),
		Name:         s.Name(),
		StartTime:    s.StartTime(),
		EndTime:      s.EndTime(),
		Attributes:   attrsToMap(s.Attributes()),
	}
	if s.Status().Code == codes.Error {
		row.Status = "error"
		row.StatusMessage = s.Status().Description
	} else {
		row.Status = "ok"
	}
	return row
}

// attrsToMap flattens OTel attributes into a string map. Boolean
// and numeric attributes get a fmt.Sprintf("%v", ...) treatment;
// the operator's debug session is a "did this span see X?" check,
// not a query for the precise value.
func attrsToMap(attrs []attribute.KeyValue) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[string(a.Key)] = attrValueString(a.Value)
	}
	return out
}

// attrValueString is the canonical OTel value→string projection for
// the trace ring's JSON shape.
func attrValueString(v attribute.Value) string {
	switch v.Type() {
	case attribute.STRING:
		return v.AsString()
	case attribute.BOOL:
		return strings.TrimSpace(fmt.Sprintf("%v", v.AsBool()))
	case attribute.INT64:
		return strings.TrimSpace(fmt.Sprintf("%v", v.AsInt64()))
	case attribute.FLOAT64:
		return strings.TrimSpace(fmt.Sprintf("%v", v.AsFloat64()))
	default:
		return v.Emit()
	}
}

// earliestStart returns the earliest StartTime across spans, or now
// if spans is empty.
func earliestStart(spans []SpanRow, now time.Time) time.Time {
	if len(spans) == 0 {
		return now
	}
	earliest := spans[0].StartTime
	for _, s := range spans[1:] {
		if s.StartTime.Before(earliest) {
			earliest = s.StartTime
		}
	}
	return earliest
}

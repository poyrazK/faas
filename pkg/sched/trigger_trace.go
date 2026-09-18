package sched

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const maxTriggerTraceLinks = 16

type triggerHeaderCarrier map[string]string

func (c triggerHeaderCarrier) Get(key string) string {
	for candidate, value := range c {
		if strings.EqualFold(candidate, key) {
			return value
		}
	}
	return ""
}

func (c triggerHeaderCarrier) Set(key, value string) {
	c[key] = value
}

func (c triggerHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

// triggerDispatchParent extracts W3C context from broker headers. A batch can
// contain records from more than one producer, so the first valid context is
// used as the parent and the remaining valid contexts are represented as links.
// The link cap keeps broker-controlled headers from creating unbounded span data.
func triggerDispatchParent(ctx context.Context, records []SourceRecord) (context.Context, []oteltrace.Link) {
	parent := ctx
	links := make([]oteltrace.Link, 0, min(len(records), maxTriggerTraceLinks))
	for _, record := range records {
		extracted := otel.GetTextMapPropagator().Extract(ctx, propagation.TextMapCarrier(triggerHeaderCarrier(record.Headers)))
		spanContext := oteltrace.SpanContextFromContext(extracted)
		if !spanContext.IsValid() {
			continue
		}
		if len(links) == 0 {
			parent = extracted
		}
		if len(links) < maxTriggerTraceLinks {
			links = append(links, oteltrace.Link{SpanContext: spanContext})
		}
	}
	return parent, links
}

func startTriggerSpan(
	ctx context.Context,
	name string,
	kind oteltrace.SpanKind,
	links []oteltrace.Link,
	attrs ...attribute.KeyValue,
) (context.Context, oteltrace.Span) {
	options := []oteltrace.SpanStartOption{
		oteltrace.WithSpanKind(kind),
		oteltrace.WithAttributes(attrs...),
	}
	if len(links) > 0 {
		options = append(options, oteltrace.WithLinks(links...))
	}
	return otel.GetTracerProvider().Tracer("gregale/trigger").Start(ctx, name, options...)
}

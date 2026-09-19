package trace

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// InjectHeaders returns the bounded platform propagation envelope stored on
// durable queue messages. The W3C fields are injected by the configured global
// propagator; the canonical Gregale trace id is added for indexed lookup.
func InjectHeaders(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if spanContext := SpanFromContext(ctx).SpanContext(); spanContext.IsValid() {
		carrier[api.TraceIDHeader] = spanContext.TraceID().String()
	}
	return map[string]string(carrier)
}

// ExtractHeaders restores a queue producer's W3C context before a delivery
// span is created. Unknown fields remain ignored by the OTel propagator.
func ExtractHeaders(ctx context.Context, headers map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headers))
}

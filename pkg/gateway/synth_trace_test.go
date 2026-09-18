// adr: 127

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInvocationDispatchBatchStitchesTraceIntoInvocation(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	server, _ := newSynthServer(t)
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "source")
	reqBody, err := json.Marshal(batchDispatchRequest{
		InvocationID: "inv-1",
		AppID:        "app-1",
		Source:       "esm",
		TriggerID:    "trigger-1",
		Records: []batchDispatchRecord{{
			ItemIdentifier: "record-1",
			PayloadB64:     "aGVsbG8=",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/invocations:dispatch_batch", bytes.NewReader(reqBody))
	otel.GetTextMapPropagator().Inject(rootCtx, propagation.HeaderCarrier(req.Header))
	response := httptest.NewRecorder()
	server.handleInvocationDispatchBatch(response, req)
	root.End()
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	spans := recorder.Ended()
	var batch, invocation sdktrace.ReadOnlySpan
	for _, span := range spans {
		switch span.Name() {
		case "gregale.trigger.batch":
			batch = span
		case "gregale.invocation":
			invocation = span
		}
	}
	if batch == nil || invocation == nil {
		t.Fatalf("spans = %v, want batch and invocation spans", spans)
	}
	if batch.Parent().TraceID() != root.SpanContext().TraceID() {
		t.Fatalf("batch parent trace id = %s, want %s", batch.Parent().TraceID(), root.SpanContext().TraceID())
	}
	if invocation.Parent().SpanID() != batch.SpanContext().SpanID() {
		t.Fatalf("invocation parent = %s, want batch span %s", invocation.Parent().SpanID(), batch.SpanContext().SpanID())
	}
}

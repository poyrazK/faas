package wire_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

func TestTraceGRPCOptionsPropagateOneTraceAcrossBothBoundaries(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(wire.TraceServerOptions()...)
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	}
	dialOptions = append(dialOptions, wire.TraceDialOptions()...)
	conn, err := grpc.NewClient("passthrough:///otelgrpc-test", dialOptions...)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	rootCtx, rootSpan := otel.Tracer("wire-test").Start(context.Background(), "root")
	_, err = healthpb.NewHealthClient(conn).Check(rootCtx, &healthpb.HealthCheckRequest{})
	rootSpan.End()
	if err != nil {
		t.Fatalf("health Check: %v", err)
	}

	// The server span ends in the gRPC stats handler after the response is
	// written, so the client can return first; on a loaded CI runner the
	// recorder briefly held only root + client (pure-Go shard 1, #4277).
	spans := recorder.Ended()
	for deadline := time.Now().Add(5 * time.Second); len(spans) < 3 && time.Now().Before(deadline); spans = recorder.Ended() {
		time.Sleep(10 * time.Millisecond)
	}
	if len(spans) < 3 {
		t.Fatalf("recorded spans = %d, want root + client + server", len(spans))
	}
	traceID := rootSpan.SpanContext().TraceID()
	for _, span := range spans {
		if span.SpanContext().TraceID() != traceID {
			t.Errorf("span %q has trace ID %s, want %s", span.Name(), span.SpanContext().TraceID(), traceID)
		}
	}
}

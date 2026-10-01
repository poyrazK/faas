// adr: 375
package wire_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/metadata"
)

func TestRequestCorrelationAcrossRepeatedHops(t *testing.T) {
	prior := wire.CorrelationFields{RequestID: "prior-request", WakeID: "prior-wake", AppID: "prior-app", DeploymentID: "prior-revision",
		InstanceID: "prior-instance", NodeID: "prior-node", InvocationID: "prior-invocation", TenantID: "prior-account", Region: "prior-region",
		CommitSHA: "prior-commit", DeploymentTag: "prior-tag", DeploymentCreatedAt: "prior-created", ImageDigest: "prior-image",
		TraceID: "prior-trace", SpanID: "prior-span", Trigger: "prior-trigger", TriggerClass: "prior-class", QueuedCount: 3, ConcurrencyAtAdmit: 4}
	base := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("x-transport", "retained", "traceparent", "retained-trace"))
	base = wire.WithCorrelationOutgoing(base, prior)
	base = wire.WithCorrelationOutgoing(base, prior) // A previous hop left duplicate values.
	original, _ := metadata.FromOutgoingContext(base)
	ctx := base
	for hop := range 64 {
		want := wire.CorrelationFields{RequestID: "request", WakeID: "causal-wake", InvocationID: "invocation", TraceID: "trace", SpanID: "span",
			AppID: "target", TenantID: "account", DeploymentID: "revision", InstanceID: fmt.Sprintf("instance-%d", hop), NodeID: "node"}
		ctx = wire.WithContext(ctx, want)
		ctx = wire.WithRequestCorrelationOutgoing(ctx)
		md, _ := metadata.FromOutgoingContext(ctx)
		got, _ := wire.CorrelationFromIncoming(metadata.NewIncomingContext(ctx, md))
		if got != want {
			t.Fatalf("hop %d correlation=%+v want=%+v", hop, got, want)
		}
		if len(md) != 12 || !reflect.DeepEqual(md.Get("x-transport"), []string{"retained"}) || !reflect.DeepEqual(md.Get("traceparent"), []string{"retained-trace"}) {
			t.Fatalf("hop %d metadata keys=%d or transport values changed", hop, len(md))
		}
		for key, values := range md {
			if len(values) != 1 {
				t.Fatalf("hop %d key %s accumulated %d values", hop, key, len(values))
			}
		}
	}
	unchanged, _ := metadata.FromOutgoingContext(base)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("preparation mutated the previous hop's metadata")
	}
}

func TestRequestCorrelationPreservesLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	ctx = wire.WithContext(ctx, wire.CorrelationFields{RequestID: "request", WakeID: "wake", Trigger: "http", TriggerClass: "request", QueuedCount: 2, ConcurrencyAtAdmit: 3})
	outgoing := wire.WithRequestCorrelationOutgoing(ctx)
	if actual, ok := outgoing.Deadline(); !ok || !actual.Equal(deadline) {
		t.Fatal("correlation publication changed the request deadline")
	}
	md, _ := metadata.FromOutgoingContext(outgoing)
	got, _ := wire.CorrelationFromIncoming(metadata.NewIncomingContext(outgoing, md))
	want, _ := wire.FromContext(ctx)
	if got != want {
		t.Fatalf("causal envelope=%+v want=%+v", got, want)
	}
	cancel()
	if outgoing.Err() != context.Canceled {
		t.Fatal("correlation publication detached request cancellation")
	}
}

func TestRequestCorrelationLegacyContextUnchanged(t *testing.T) {
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("x-faas-instance-id", "legacy", "x-transport", "retained"))
	if wire.WithRequestCorrelationOutgoing(ctx) != ctx {
		t.Fatal("publication without a canonical envelope replaced the context")
	}
}

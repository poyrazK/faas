package wire_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/metadata"
)

// TestPlatformIdentityObservabilityEnvelopeAcrossHops pins the complete
// gateway -> schedd -> vmmd observability envelope. The metadata conversions
// below are the same conversions performed by grpc-go at each real RPC
// boundary; keeping both boundaries in one test prevents a field from being
// added to one hop while silently disappearing on the next.
func TestPlatformIdentityObservabilityEnvelopeAcrossHops(t *testing.T) {
	identity := api.PlatformIdentity{
		RequestID:           "request-1",
		AppID:               "app-1",
		DeploymentID:        "deployment-1",
		TenantID:            "tenant-1",
		InstanceID:          "instance-1",
		NodeID:              "node-1",
		Region:              "eu-west",
		CommitSHA:           "abc123",
		DeploymentTag:       "canary",
		DeploymentCreatedAt: "2026-09-19T19:00:00Z",
		ImageDigest:         "sha256:digest",
	}
	wantIdentity := wire.CorrelationFieldsFromPlatformIdentity(identity)
	want := wantIdentity
	want.WakeID = "wake-1"
	want.InvocationID = "invocation-1"
	want.TraceID = "trace-1"
	want.SpanID = "span-1"
	want.Trigger = "gateway"
	want.TriggerClass = "http"
	want.QueuedCount = 3
	want.ConcurrencyAtAdmit = 2

	// Gateway stamps the selected identity and request lifecycle onto its
	// context before making the schedd RPC.
	gatewayCtx := wire.WithPlatformIdentity(context.Background(), identity)
	gatewayFields, ok := wire.FromContext(gatewayCtx)
	if !ok {
		t.Fatal("gateway identity was not stored on context")
	}
	gatewayFields.WakeID = want.WakeID
	gatewayFields.InvocationID = want.InvocationID
	gatewayFields.TraceID = want.TraceID
	gatewayFields.SpanID = want.SpanID
	gatewayFields.Trigger = want.Trigger
	gatewayFields.TriggerClass = want.TriggerClass
	gatewayFields.QueuedCount = want.QueuedCount
	gatewayFields.ConcurrencyAtAdmit = want.ConcurrencyAtAdmit

	assertCorrelationFields(t, gatewayFields, want, "gateway")
	assertLogFields(t, gatewayFields, want)
	assertTraceAttributes(t, pkgtrace.PlatformIdentityAttributes(identity), identity)

	// grpc-go turns the outgoing metadata into incoming metadata at schedd.
	scheddIncoming := incomingFromOutgoing(wire.WithCorrelationOutgoing(context.Background(), gatewayFields))
	scheddFields, ok := wire.CorrelationFromIncoming(scheddIncoming)
	if !ok {
		t.Fatal("schedd did not receive gateway correlation metadata")
	}
	// This is the production server lift followed by the scheduler's
	// identity projection. It must preserve lifecycle and trace fields.
	scheddCtx := wire.WithPlatformIdentity(wire.WithContext(scheddIncoming, scheddFields), identity)
	scheddFields, ok = wire.FromContext(scheddCtx)
	if !ok {
		t.Fatal("schedd did not retain correlation context")
	}
	assertCorrelationFields(t, scheddFields, want, "schedd")

	// Schedd adds no new identity fields before calling vmmd; it forwards the
	// same envelope over the second gRPC boundary.
	vmmdIncoming := incomingFromOutgoing(wire.WithCorrelationOutgoing(context.Background(), scheddFields))
	vmmdFields, ok := wire.CorrelationFromIncoming(vmmdIncoming)
	if !ok {
		t.Fatal("vmmd did not receive schedd correlation metadata")
	}
	assertCorrelationFields(t, vmmdFields, want, "vmmd")
}

func incomingFromOutgoing(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return context.Background()
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

func assertCorrelationFields(t *testing.T, got, want wire.CorrelationFields, hop string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s correlation fields = %+v, want %+v", hop, got, want)
	}
}

func assertLogFields(t *testing.T, fields, want wire.CorrelationFields) {
	t.Helper()
	var buf bytes.Buffer
	logger := wire.NewCorrelationLogger(slog.New(slog.NewJSONHandler(&buf, nil)), fields, "gatewayd-internal")
	logger.Info("dispatch")
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("decode structured log: %v", err)
	}
	expected := map[string]string{
		"request_id": want.RequestID, "wake_id": want.WakeID, "app_id": want.AppID,
		"deployment_id": want.DeploymentID, "instance_id": want.InstanceID, "node_id": want.NodeID,
		"invocation_id": want.InvocationID, "tenant_id": want.TenantID, "region": want.Region,
		"commit_sha": want.CommitSHA, "deployment_tag": want.DeploymentTag,
		"deployment_created_at": want.DeploymentCreatedAt, "image_digest": want.ImageDigest,
		"trace_id": want.TraceID, "span_id": want.SpanID,
	}
	for key, value := range expected {
		if got, _ := record[key].(string); got != value {
			t.Errorf("log field %q = %q, want %q", key, got, value)
		}
	}
}

func assertTraceAttributes(t *testing.T, attrs []attribute.KeyValue, identity api.PlatformIdentity) {
	t.Helper()
	got := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		got[string(attr.Key)] = attr.Value.AsString()
	}
	expected := map[string]string{
		"request_id": identity.RequestID, "app_id": identity.AppID,
		"deployment_id": identity.DeploymentID, "tenant_id": identity.TenantID,
		"instance_id": identity.InstanceID, "node_id": identity.NodeID,
		"region": identity.Region, "commit_sha": identity.CommitSHA,
		"deployment_tag":        identity.DeploymentTag,
		"deployment_created_at": identity.DeploymentCreatedAt,
		"image_digest":          identity.ImageDigest,
	}
	if len(got) != len(expected) {
		t.Fatalf("trace attributes = %v, want %v", got, expected)
	}
	for key, value := range expected {
		if got[key] != value {
			t.Errorf("trace attribute %q = %q, want %q", key, got[key], value)
		}
	}
}

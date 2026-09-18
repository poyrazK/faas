package vmmdgrpc

import (
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"go.opentelemetry.io/otel/attribute"
)

// adr: 127
func TestGuestTransportAttributesAreBoundedAndRedacted(t *testing.T) {
	attrs := guestTransportAttributes(&vmmdpb.ForwardHTTPRequestInit{
		Instance:    "instance-1",
		Method:      "POST",
		RequestUri:  "/private?token=secret",
		AppProtocol: "grpc",
	}, "h2c", 8443)

	for _, attr := range attrs {
		value := attr.Value.AsString()
		if strings.Contains(value, "private") || strings.Contains(value, "secret") || strings.Contains(value, "POST") {
			t.Fatalf("guest transport span attribute leaked request data: %s=%q", attr.Key, value)
		}
	}

	got := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		got[string(attr.Key)] = attr.Value
	}
	if got["gregale.dependency.type"].AsString() != "guest_transport" {
		t.Fatalf("dependency type = %q, want guest_transport", got["gregale.dependency.type"].AsString())
	}
	if got[guestTransportProtocolKey].AsString() != "h2c" || got[guestTransportAppProtoKey].AsString() != "grpc" {
		t.Fatalf("guest protocol attrs = %#v", got)
	}
	if got[guestTransportPortKey].AsInt64() != 8443 {
		t.Fatalf("guest port = %d, want 8443", got[guestTransportPortKey].AsInt64())
	}
}

func TestGuestTransportAttributesNilInit(t *testing.T) {
	attrs := guestTransportAttributes(nil, "h1", 8080)
	if len(attrs) != 4 {
		t.Fatalf("nil init attrs = %d, want 4", len(attrs))
	}
}

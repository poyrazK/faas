package vmmdgrpc

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dependencytrace"
)

const (
	guestTransportDependencyType = "guest_transport"
	guestTransportKindKey        = "gregale.dependency.kind"
	guestTransportProtocolKey    = "gregale.guest.transport.protocol"
	guestTransportAppProtoKey    = "gregale.guest.app_protocol"
	guestTransportPortKey        = "gregale.guest.port"
	guestTransportInstanceKey    = "gregale.guest.instance_id"
)

// instrumentGuestTransport adds the platform-owned client span for the
// vmmd-to-guest bridge. The RoundTripper sees only the bridge socket, so the
// bounded guest metadata is supplied explicitly; request URIs, headers, and
// bodies remain excluded by dependencytrace's redaction boundary.
func instrumentGuestTransport(base http.RoundTripper, init *vmmdpb.ForwardHTTPRequestInit, protocol string, port uint32) http.RoundTripper {
	attrs := guestTransportAttributes(init, protocol, port)
	return dependencytrace.NewDependencyTransport(base, attrs...)
}

func guestTransportAttributes(init *vmmdpb.ForwardHTTPRequestInit, protocol string, port uint32) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gregale.dependency.type", guestTransportDependencyType),
		attribute.String(guestTransportKindKey, "vmmd_guest_bridge"),
		attribute.String(guestTransportProtocolKey, protocol),
		attribute.Int(guestTransportPortKey, int(port)),
	}
	if init == nil {
		return attrs
	}
	if appProtocol := init.GetAppProtocol(); appProtocol != "" {
		attrs = append(attrs, attribute.String(guestTransportAppProtoKey, appProtocol))
	}
	if instance := init.GetInstance(); instance != "" {
		attrs = append(attrs, attribute.String(guestTransportInstanceKey, instance))
	}
	return attrs
}

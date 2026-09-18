package outbound

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/onebox-faas/faas/pkg/dependencytrace"
)

const (
	dependencyTypeKey = "gregale.dependency.type"
	integrationIDKey  = "gregale.outbound.integration_id"
	appIDKey          = "gregale.outbound.app_id"
	originHostKey     = "gregale.outbound.origin_host"
	originSchemeKey   = "gregale.outbound.origin_scheme"
)

func dependencySpanAttributes(integration Integration, appID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(dependencyTypeKey, "outbound_integration"),
		attribute.String(integrationIDKey, integration.ID),
		attribute.String(appIDKey, appID),
	}
	if integration.Origin != nil {
		// Keep destination metadata bounded and credential-free. The request
		// path and query are intentionally not copied into the binding span.
		attrs = append(attrs,
			attribute.String(originHostKey, integration.Origin.Hostname()),
			attribute.String(originSchemeKey, integration.Origin.Scheme),
		)
	}
	return attrs
}

func newDependencyTransport(base http.RoundTripper) http.RoundTripper {
	return dependencytrace.NewDependencyTransport(base)
}

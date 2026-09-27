package outbound

import (
	"net/http"
	"strings"

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
		attribute.String("gregale.dependency.kind", "https"),
		attribute.String(integrationIDKey, integration.ID),
		attribute.String(appIDKey, appID),
	}
	if integration.AccountID != "" {
		// The caller cannot set this identity. PostgresResolver obtains it
		// from the integration row; the retained-span exporter uses it only
		// for apid's tenant-scoped writer and removes it before persistence.
		attrs = append(attrs, attribute.String("gregale.internal.account_id", integration.AccountID))
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

func dependencySpanName(integration Integration) string {
	name := strings.TrimSpace(integration.Name)
	if len(name) == 0 || len(name) > 63 {
		return "outbound.integration"
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && (i == 0 || ch != '-') {
			return "outbound.integration"
		}
	}
	return "outbound." + name
}

func newDependencyTransport(base http.RoundTripper) http.RoundTripper {
	return dependencytrace.NewDependencyTransport(base)
}

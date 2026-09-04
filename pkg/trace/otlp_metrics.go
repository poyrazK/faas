// pkg/trace/otlp_metrics.go — Prometheus→OTLP metrics bridge facade.
package trace

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

// BridgePrometheusToOTLP bridges a daemon Prometheus registry to an OTLP
// collector over HTTP. The bridge is best-effort and returns a bounded
// shutdown function; an unavailable collector never makes daemon startup
// fail. The daemon bootstrap path uses the implementation in pkg/wire so it
// can attach the bridge to every OpsMetrics instance without an import cycle.
func BridgePrometheusToOTLP(
	ctx context.Context,
	registry *prometheus.Registry,
	endpoint string,
	log *slog.Logger,
) (shutdown func(context.Context) error, err error) {
	return wire.StartOTLPMetrics(ctx, registry, endpoint, "faas", log)
}

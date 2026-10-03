package routehealth

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func HistoryPolicy() api.RouteHealthEvaluationPolicy {
	return api.RouteHealthEvaluationPolicy{
		Version: api.RouteHealthEvaluationVersion, Windows: api.RouteHealthWindows,
		WindowSeconds: int64(api.RouteHealthWindow / time.Second), IngestionLagSeconds: int64(api.RouteHealthIngestionLag / time.Second),
		MinimumRequests: api.RouteHealthMinRequests, MinimumErrors: api.RouteHealthMinErrors,
		ErrorRateFloor: api.RouteHealthErrorRateFloor, ErrorRateDelta: api.RouteHealthErrorRateDelta, ErrorRateFactor: api.RouteHealthErrorRateFactor,
		MinLatencyRequests: api.RouteHealthMinLatencyRequests, LatencyQuantile: api.RouteHealthLatencyQuantile,
		LatencyFactor: api.RouteHealthLatencyFactor, LatencyDeltaMS: api.RouteHealthLatencyDeltaMS,
		ComparisonEpsilon: api.RouteHealthComparisonEpsilon,
	}
}

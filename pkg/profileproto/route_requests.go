package profileproto

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

const RouteRequestHeader = "X-Gregale-Route-Requests"

func DecodeRouteRequestHeader(value string) (*RouteRequestReport, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > base64.RawURLEncoding.EncodedLen(api.ProfileRouteRequestReportMaxBytes) {
		return nil, fmt.Errorf("route request report exceeds bounds")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var report RouteRequestReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	if len(report.Routes) > api.ProfileRouteMaxLabels || report.FromUnixNano <= 0 || report.UntilUnixNano <= report.FromUnixNano || report.UntilUnixNano-report.FromUnixNano > int64(api.ProfileMaxCaptureDuration) {
		return nil, fmt.Errorf("invalid route request report")
	}
	for route, count := range report.Routes {
		if route == "" || route == api.ProfileUnattributedRoute || !api.ValidProfileRoute(route) || count < 0 || count > api.ProfileRouteMaxLabeledRequests {
			return nil, fmt.Errorf("invalid route request count")
		}
	}
	return &report, nil
}

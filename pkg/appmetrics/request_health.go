package appmetrics

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// FetchRequestHealth reads only count, server-error count and sample freshness.
// Health does not depend on unrelated latency, cache, CPU or fleet wake queries.
// Both counts use the same histogram population and window. 4xx responses do
// not count as failures, but remain in the total request denominator.
func FetchRequestHealth(ctx context.Context, client *promql.Client, appID string) (api.AppMetricsResponse, string) {
	out := api.AppMetricsResponse{AppID: appID, Range: api.AppHealthMetricsRange}
	if client == nil || strings.ContainsAny(appID, "\"\n\\") {
		return out, SourceDegraded
	}
	selector := fmt.Sprintf(`gateway_request_duration_seconds_count{app=%q}`, appID)
	count, err := client.QueryScalar(ctx, fmt.Sprintf(`sum(increase(%s[%s]))`, selector, out.Range))
	if err != nil || !validHealthCount(count) {
		return out, SourceDegraded
	}
	failures, err := client.QueryScalar(ctx, fmt.Sprintf(`sum(increase(gateway_request_duration_seconds_count{app=%q,class="5xx"}[%s]))`, appID, out.Range))
	if err != nil || !validHealthCount(failures) || failures > count {
		return out, SourceDegraded
	}
	seconds, err := client.QueryScalar(ctx, fmt.Sprintf(`max(timestamp(%s))`, selector))
	if err != nil || !validHealthCount(seconds) {
		return out, SourceDegraded
	}
	out.RequestCount = int64(SafeRoundNonNeg(count))
	if count > 0 {
		out.ErrorRatePct = failures / count * 100
	}
	if seconds > 0 {
		out.AsOf = time.Unix(0, int64(seconds*float64(time.Second))).UTC().Format(time.RFC3339Nano)
	}
	return out, SourcePrometheus
}

func validHealthCount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < float64(math.MaxInt64)
}

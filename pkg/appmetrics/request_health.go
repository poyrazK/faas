package appmetrics

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// RequestHealth is confirmed only when Source is Prometheus. Reason is a safe
// closed vocabulary; raw query/backend errors never enter the health response.
type RequestHealth struct {
	RequestCount int64
	ServerErrors int64
	ErrorRatePct float64
	AsOf         string
	Source       string
	Reason       string
}

// FetchRequestHealth reads the current serving releases only. Deployment label
// overflow and pre-routing requests cannot be assigned to an environment; they
// prevent confirmation rather than falling back to app-wide counters. All
// counts share a window; coverage requires two samples for every selected
// deployment, and freshness is the oldest selected deployment's latest sample.
func FetchRequestHealth(ctx context.Context, client *promql.Client, appID string, deploymentIDs []string) RequestHealth {
	out := RequestHealth{Source: SourceDegraded, Reason: "request_evidence_unavailable"}
	ids := slices.Clone(deploymentIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if client == nil {
		return out
	}
	if appID == "" || len(ids) == 0 || len(ids) > api.AppHealthMetricsDeploymentLimit {
		out.Reason = "request_scope_unavailable"
		return out
	}
	labels := make([]string, len(ids))
	for n, id := range ids {
		if id == "" || id == "__other__" {
			out.Reason = "request_scope_unavailable"
			return out
		}
		labels[n] = regexp.QuoteMeta(id)
	}
	selector := fmt.Sprintf(`gateway_request_duration_by_deployment_seconds_count{app=%q,deployment=~%q}`, appID, strings.Join(labels, "|"))
	count, err := client.QueryScalar(ctx, fmt.Sprintf(`sum(increase(%s[%s]))`, selector, api.AppHealthMetricsRange))
	if err != nil || !validHealthCount(count) {
		return out
	}
	errorSelector := strings.TrimSuffix(selector, "}") + `,class="5xx"}`
	failures, err := client.QueryScalar(ctx, fmt.Sprintf(`(sum(increase(%s[%s])) or vector(0))`, errorSelector, api.AppHealthMetricsRange))
	if err != nil || !validHealthCount(failures) || failures > count {
		return out
	}
	// A new status-class series with only one scrape cannot produce an
	// increase. Exclude that partial coverage instead of hiding its errors.
	coverageQuery := fmt.Sprintf(`count(count by (deployment) (increase(%s[%s])) and on (deployment) count by (deployment) (%s)) - (count(%s unless (count_over_time(%s[%s]) >= %d)) or vector(0))`, selector, api.AppHealthMetricsRange, selector, selector, selector, api.AppHealthMetricsRange, api.AppHealthCounterMinSamples)
	coverage, err := client.QueryScalar(ctx, coverageQuery)
	if err != nil {
		return out
	}
	if coverage != float64(len(ids)) {
		out.Reason = "request_coverage_incomplete"
		return out
	}
	seconds, err := client.QueryScalar(ctx, fmt.Sprintf(`min(max by (deployment) (timestamp(%s)))`, selector))
	if err != nil || !validHealthCount(seconds) || seconds <= 0 || seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return out
	}
	unassignedSelector := fmt.Sprintf(`gateway_request_duration_by_deployment_seconds_count{app=%q,deployment=~"|__other__"}`, appID)
	// Include the current value of a newly introduced unattributed series;
	// otherwise its first request could disappear behind an empty increase.
	unassignedQuery := fmt.Sprintf(`(sum(increase(%s[%s])) or vector(0)) + (sum(%s unless (count_over_time(%s[%s]) >= %d)) or vector(0))`, unassignedSelector, api.AppHealthMetricsRange, unassignedSelector, unassignedSelector, api.AppHealthMetricsRange, api.AppHealthCounterMinSamples)
	unassigned, err := client.QueryScalar(ctx, unassignedQuery)
	if err != nil || !validHealthCount(unassigned) {
		return out
	}
	if unassigned > 0 {
		out.Reason = "request_coverage_incomplete"
		return out
	}
	out.RequestCount = int64(SafeRoundNonNeg(count))
	out.ServerErrors = int64(SafeRoundNonNeg(failures))
	if count > 0 {
		out.ErrorRatePct = failures / count * 100
	}
	out.AsOf = time.Unix(0, int64(seconds*float64(time.Second))).UTC().Format(time.RFC3339Nano)
	out.Source, out.Reason = SourcePrometheus, ""
	return out
}

func validHealthCount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < float64(math.MaxInt64)
}
